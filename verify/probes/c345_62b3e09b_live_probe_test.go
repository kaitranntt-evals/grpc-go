// How to run (branch evalon/grpc-go-tr-62b3e09b): cp this file to internal/transport/verify_c345_live_probe_test.go && go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
//
// Probes for claims C3, C4, C5: does receive buffering *as wired by the
// solution's real transports* (http2Client.newStream / http2Server.operateHeaders:
// buf.init() followed by buf.enableCompaction(t.bufferPool)) compact tiny
// payloads, as opposed to a bare recvBuffer.init() (what the eval fixture calls)?
package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

type vSnap struct {
	chanMsgs, backlogEntries, tailBytes, payloadBytes, distinctBackings int
	maxEntryLen                                                         int
}

func (s vSnap) String() string {
	return fmt.Sprintf("chan=%d backlogEntries=%d tailBytes=%d unreadPayloadBytes=%d distinctBackingArrays=%d largestEntryLen=%d",
		s.chanMsgs, s.backlogEntries, s.tailBytes, s.payloadBytes, s.distinctBackings, s.maxEntryLen)
}

// vSnapshot inspects the queued storage of a recvBuffer on branch 62b3e09b.
func vSnapshot(b *recvBuffer) vSnap {
	b.mu.Lock()
	defer b.mu.Unlock()
	var s vSnap
	ptrs := map[unsafe.Pointer]bool{}
	account := func(m recvMsg) {
		if m.buffer == nil {
			return
		}
		d := m.buffer.ReadOnlyData()
		s.payloadBytes += len(d)
		if len(d) > s.maxEntryLen {
			s.maxEntryLen = len(d)
		}
		if len(d) > 0 {
			ptrs[unsafe.Pointer(unsafe.SliceData(d))] = true
		}
	}
	select {
	case m := <-b.c:
		s.chanMsgs = 1
		account(m)
		b.c <- m
	default:
	}
	for _, m := range b.backlog {
		account(m)
	}
	s.backlogEntries = len(b.backlog)
	if b.tail != nil {
		s.tailBytes = b.tailLen
		s.payloadBytes += b.tailLen
		if b.tailLen > s.maxEntryLen {
			s.maxEntryLen = b.tailLen
		}
		ptrs[unsafe.Pointer(unsafe.SliceData(*b.tail))] = true
	}
	s.distinctBackings = len(ptrs)
	return s
}

// vPeer is a raw HTTP/2 server that answers one stream and then sends DATA
// frames on demand. Every socket operation is bounded by a deadline.
type vPeer struct {
	t        *testing.T
	mu       sync.Mutex
	fr       *http2.Framer
	streamID uint32
	acks     chan [8]byte
	seq      byte
}

func (p *vPeer) send(payloads [][]byte) {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pl := range payloads {
		if err := p.fr.WriteData(p.streamID, false, pl); err != nil {
			p.t.Fatalf("WriteData: %v", err)
		}
	}
}

// sync returns once the client transport has processed every frame sent so far.
func (p *vPeer) sync() {
	p.t.Helper()
	p.mu.Lock()
	p.seq++
	want := [8]byte{'v', 'e', 'r', 'i', 'f', 'y', 0, p.seq}
	err := p.fr.WritePing(false, want)
	p.mu.Unlock()
	if err != nil {
		p.t.Fatalf("WritePing: %v", err)
	}
	for {
		select {
		case got := <-p.acks:
			if got == want {
				return
			}
		case <-time.After(10 * time.Second):
			p.t.Fatalf("timed out waiting for ping ack")
		}
	}
}

// vLiveClientStream creates a real http2Client and a real ClientStream (so the
// stream's recvBuffer is initialized exactly as the solution ships it).
func vLiveClientStream(t *testing.T) (*ClientStream, *vPeer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })
	lis.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second))
	p := &vPeer{t: t, acks: make(chan [8]byte, 16)}
	ready := make(chan struct{})
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := io.ReadFull(conn, make([]byte, len(clientPreface))); err != nil {
			return
		}
		p.mu.Lock()
		p.fr = http2.NewFramer(conn, conn)
		p.fr.WriteSettings()
		p.mu.Unlock()
		for {
			f, err := p.fr.ReadFrame()
			if err != nil {
				return
			}
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					p.mu.Lock()
					p.fr.WriteSettingsAck()
					p.mu.Unlock()
				}
			case *http2.HeadersFrame:
				var hbuf bytes.Buffer
				enc := hpack.NewEncoder(&hbuf)
				enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
				enc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"})
				p.mu.Lock()
				p.streamID = f.StreamID
				p.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: f.StreamID, BlockFragment: hbuf.Bytes(), EndHeaders: true})
				p.mu.Unlock()
				close(ready)
			case *http2.PingFrame:
				if f.IsAck() {
					p.acks <- f.Data
				} else {
					p.mu.Lock()
					p.fr.WritePing(true, f.Data)
					p.mu.Unlock()
				}
			}
		}
	}()
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatalf("NewHTTP2Client: %v", err)
	}
	t.Cleanup(func() { ct.Close(fmt.Errorf("verify probe done")) })
	cs, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "/verify/probe"}, nil)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("timed out waiting for the peer to answer the stream")
	}
	return cs, p
}

func vReadExactly(t *testing.T, cs *ClientStream, n int) []byte {
	t.Helper()
	data, err := cs.Read(n)
	if err != nil {
		t.Fatalf("ClientStream.Read(%d): %v", n, err)
	}
	defer data.Free()
	return data.Materialize()
}

// C3: burst of 1026 one-byte payloads (the fixture's TestEval_RecvBufferCompaction burst), no reads.
func TestVerifyC3_BurstNoReads(t *testing.T) {
	const n = 1024 + 2
	payloads := make([][]byte, n)
	var want []byte
	for i := range payloads {
		payloads[i] = []byte{byte(i%251 + 1)}
		want = append(want, payloads[i]...)
	}

	// (a) What the eval fixture builds: a bare recvBuffer.init().
	bare := &recvBuffer{}
	bare.init()
	for _, p := range payloads {
		bare.put(recvMsg{buffer: mem.Copy(p, mem.DefaultBufferPool())})
	}
	t.Logf("C3 bare init() only (fixture path), %d one-byte puts, no reads: %v", n, vSnapshot(bare))

	// (b) The recvBuffer of a real stream created by the solution's http2Client,
	// fed by real one-byte DATA frames off the wire.
	cs, peer := vLiveClientStream(t)
	peer.send(payloads)
	peer.sync()
	snap := vSnapshot(&cs.buf)
	t.Logf("C3 live http2Client stream, %d one-byte DATA frames, no reads: %v", n, snap)
	if snap.payloadBytes != n {
		t.Fatalf("live stream holds %d unread bytes, want %d", snap.payloadBytes, n)
	}
	if entries := snap.backlogEntries; entries > n/16 {
		t.Errorf("C3 PROBLEM REPRODUCED on the live stream: %d backlog entries for %d payloads", entries, n)
	}
	if snap.distinctBackings > 3 {
		t.Errorf("C3 PROBLEM REPRODUCED on the live stream: %d distinct backing arrays for %d payloads", snap.distinctBackings, n)
	}
	if got := vReadExactly(t, cs, n); !bytes.Equal(got, want) {
		t.Errorf("live stream delivered wrong bytes")
	} else {
		t.Logf("C3 live stream delivered all %d bytes in order", n)
	}
}

// C4: mixed-size (1..7 byte) payloads (the fixture's MixedFrames burst), no reads.
func TestVerifyC4_MixedSizes(t *testing.T) {
	const n = 1024 + 50
	payloads := make([][]byte, n)
	var want []byte
	for i := range payloads {
		size := (i % 7) + 1
		p := make([]byte, size)
		for j := range p {
			p[j] = byte((i*13+j)%251 + 1)
		}
		payloads[i] = p
		want = append(want, p...)
	}
	bare := &recvBuffer{}
	bare.init()
	for _, p := range payloads {
		bare.put(recvMsg{buffer: mem.Copy(p, mem.DefaultBufferPool())})
	}
	t.Logf("C4 bare init() only (fixture path), %d mixed 1..7-byte puts: %v", n, vSnapshot(bare))

	cs, peer := vLiveClientStream(t)
	peer.send(payloads)
	peer.sync()
	snap := vSnapshot(&cs.buf)
	t.Logf("C4 live http2Client stream, %d mixed 1..7-byte DATA frames (%d bytes): %v", n, len(want), snap)
	if snap.payloadBytes != len(want) {
		t.Fatalf("live stream holds %d unread bytes, want %d", snap.payloadBytes, len(want))
	}
	if snap.backlogEntries > n/8 || snap.distinctBackings > 3 {
		t.Errorf("C4 PROBLEM REPRODUCED on the live stream: %v", snap)
	}

	// Mixed with ordinary-sized frames: tiny runs around 8 KiB frames.
	cs2, peer2 := vLiveClientStream(t)
	var mixed [][]byte
	var want2 []byte
	for round := 0; round < 3; round++ {
		for i := 0; i < 200; i++ {
			mixed = append(mixed, []byte{byte(round*50 + i%50 + 1)})
		}
		mixed = append(mixed, bytes.Repeat([]byte{byte(0xA0 + round)}, 8192))
	}
	for _, p := range mixed {
		want2 = append(want2, p...)
	}
	peer2.send(mixed)
	peer2.sync()
	snap2 := vSnapshot(&cs2.buf)
	t.Logf("C4 live stream, 3 x (200 one-byte frames + one 8 KiB frame) = %d frames, %d bytes: %v", len(mixed), len(want2), snap2)
	if snap2.backlogEntries+1 > 8 {
		t.Errorf("C4 PROBLEM REPRODUCED on the live stream (tiny frames around large ones stay separate): %v", snap2)
	}
	if got := vReadExactly(t, cs, len(want)); !bytes.Equal(got, want) {
		t.Errorf("live stream delivered wrong bytes")
	}
	if got := vReadExactly(t, cs2, len(want2)); !bytes.Equal(got, want2) {
		t.Errorf("live stream (tiny+large) delivered wrong bytes")
	} else {
		t.Logf("C4 live streams delivered all bytes in order")
	}
}

// C5: repeated enqueue / partial-read cycles (the fixture's MultiCycleMemoryBound shape).
func TestVerifyC5_EnqueueReadCycles(t *testing.T) {
	const cycles = 3
	const framesPerCycle = 1024 + 100
	mk := func(cycle int) (payloads [][]byte, flat []byte) {
		for i := 0; i < framesPerCycle; i++ {
			size := (i % 5) + 1
			p := make([]byte, size)
			for j := range p {
				p[j] = byte((cycle*31+i*7+j)%251 + 1)
			}
			payloads = append(payloads, p)
			flat = append(flat, p...)
		}
		return
	}

	// (a) bare init(): same put / load / one-receive cycle as the fixture.
	bare := &recvBuffer{}
	bare.init()
	for c := 0; c < cycles; c++ {
		payloads, _ := mk(c)
		for _, p := range payloads {
			bare.put(recvMsg{buffer: mem.Copy(p, mem.DefaultBufferPool())})
		}
		t.Logf("C5 bare init() only (fixture path), cycle %d after enqueue: %v", c, vSnapshot(bare))
		bare.load()
		select {
		case m := <-bare.c:
			m.buffer.Free()
		default:
		}
	}

	// (b) live stream.
	cs, peer := vLiveClientStream(t)
	var want, got []byte
	for c := 0; c < cycles; c++ {
		payloads, flat := mk(c)
		want = append(want, flat...)
		peer.send(payloads)
		peer.sync()
		snap := vSnapshot(&cs.buf)
		t.Logf("C5 live stream, cycle %d after enqueueing %d frames (unread so far %d bytes): %v", c, framesPerCycle, len(want)-len(got), snap)
		if snap.backlogEntries > 1024/2 || snap.distinctBackings > 8 {
			t.Errorf("C5 PROBLEM REPRODUCED on the live stream at cycle %d: %v", c, snap)
		}
		// Intermediate read boundary: the application reads a little.
		got = append(got, vReadExactly(t, cs, 7)...)
		snap = vSnapshot(&cs.buf)
		t.Logf("C5 live stream, cycle %d after intermediate 7-byte read (unread %d bytes): %v", c, len(want)-len(got), snap)
		if snap.backlogEntries > 1024/2 || snap.distinctBackings > 8 {
			t.Errorf("C5 PROBLEM REPRODUCED on the live stream at read boundary %d: %v", c, snap)
		}
	}
	got = append(got, vReadExactly(t, cs, len(want)-len(got))...)
	if !bytes.Equal(got, want) {
		t.Errorf("live stream delivered wrong bytes across cycles")
	} else {
		t.Logf("C5 live stream delivered all %d bytes in order across %d cycles", len(want), cycles)
	}
}

// C3 (server side): a real http2Server stream fed one-byte DATA frames by a raw HTTP/2 client.
func TestVerifyC3_LiveServerStream(t *testing.T) {
	const n = 1024 + 2
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)
	defer server.stop()
	conn, err := net.Dial("tcp", server.lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := conn.Write(clientPreface); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	acked := make(chan struct{})
	want := [8]byte{'v', 'e', 'r', 'i', 'f', 'y', 'c', '3'}
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			if p, ok := f.(*http2.PingFrame); ok {
				if p.IsAck() && p.Data == want {
					close(acked)
				} else if !p.IsAck() {
					mu.Lock()
					fr.WritePing(true, p.Data)
					mu.Unlock()
				}
			}
		}
	}()
	var hbuf bytes.Buffer
	enc := hpack.NewEncoder(&hbuf)
	for _, hf := range []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":path", Value: "/verify/probe"}, {Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"}} {
		enc.WriteField(hf)
	}
	mu.Lock()
	fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true})
	for i := 0; i < n; i++ {
		fr.WriteData(1, false, []byte{byte(i%251 + 1)})
	}
	fr.WritePing(false, want)
	mu.Unlock()
	select {
	case <-acked:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the server to process the DATA frames")
	}
	var ss *ServerStream
	server.mu.Lock()
	for c := range server.conns {
		st := c.(*http2Server)
		st.mu.Lock()
		ss = st.activeStreams[1]
		st.mu.Unlock()
	}
	server.mu.Unlock()
	if ss == nil {
		t.Fatal("server stream not found")
	}
	snap := vSnapshot(&ss.buf)
	t.Logf("C3 live http2Server stream, %d one-byte DATA frames, no reads: %v", n, snap)
	if snap.payloadBytes != n || snap.backlogEntries > n/16 || snap.distinctBackings > 3 {
		t.Errorf("C3 PROBLEM REPRODUCED on the live server stream: %v", snap)
	}
}
