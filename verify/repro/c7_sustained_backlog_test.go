//go:build verify_audit

// C7 repro: copy to internal/transport/ on evalon/grpc-go-tr-718bb10b, then: go test -tags verify_audit -v -run '^TestC7_' google.golang.org/grpc/internal/transport -race -count=1

package transport

// Probe for C7 on evalon/grpc-go-tr-718bb10b: a receive schedule that always
// leaves one message queued (put 2, then loop {put 1; read 1}) on a
// production-constructed client stream, retaining delivered buffers as message
// assembly does.
// Run: go test -tags verify_audit -v -run '^TestC7_' google.golang.org/grpc/internal/transport -race -count=1

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
)

func c7Run(t *testing.T, label string, n int, traceCap bool) {
	pool := mem.DefaultBufferPool()
	s := (&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil)
	b := &s.buf
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reader := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
	var retained mem.BufferSlice
	defer retained.Free()
	var want, got []byte
	put := func(i int) {
		p := []byte{byte(i%251 + 1)}
		want = append(want, p...)
		b.put(recvMsg{buffer: mem.Copy(p, pool)})
	}
	read := func() {
		buf, err := reader.Read(1)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got = append(got, buf.ReadOnlyData()...)
		retained = append(retained, buf)
	}
	put(0)
	put(1) // one message now always stays queued behind the reader
	for i := 2; i < n; i++ {
		put(i)
		if traceCap && i <= 20 {
			b.mu.Lock()
			pc := -1
			if b.pending != nil {
				pc = cap(*b.pending)
			}
			t.Logf("C7 trace %s: after put #%d: len(backlog)=%d pendingCap=%d nextPendingCap=%d", label, i, len(b.backlog), pc, b.nextPendingCap)
			b.mu.Unlock()
		}
		read()
	}
	read()
	read()
	distinct, n16k, _ := c7Retained(retained)
	t.Logf("C7 %s: payload=%d bytes in %d one-byte frames; inOrder=%v; retained buffers=%d; >=16KiB arrays holding <=4 payload bytes=%d; distinct retained capacity=%d bytes (%.0fx payload)",
		label, len(want), n, bytes.Equal(want, got), len(retained), n16k, distinct, float64(distinct)/float64(len(want)))
}

func TestC7_SustainedBacklogDefault(t *testing.T) {
	c7Run(t, "env="+os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), 1024, true)
}

// c7Retained sums the capacity of distinct backing arrays (keyed by the
// array's end address so that split views of one array count once) and counts
// arrays of >=16KiB that carry at most 4 payload bytes.
func c7Retained(bs mem.BufferSlice) (distinct int, sparse16k int, arrays int) {
	type agg struct{ capacity, used int }
	seen := map[uintptr]*agg{}
	for _, buf := range bs {
		d := buf.ReadOnlyData()
		if len(d) == 0 {
			continue
		}
		end := uintptr(unsafe.Pointer(&d[0])) + uintptr(cap(d))
		a := seen[end]
		if a == nil {
			a = &agg{}
			seen[end] = a
		}
		a.capacity = max(a.capacity, cap(d))
		a.used += len(d)
	}
	for _, a := range seen {
		distinct += a.capacity
		if a.capacity >= 16383 && a.used <= 4 {
			sparse16k++
		}
	}
	return distinct, sparse16k, len(seen)
}

// End to end: a real http2Server stream whose application is blocked in
// ServerStream.Read(N) (assembling one N-byte message) while a raw HTTP/2
// client sends the message as one-byte DATA frames, each sent once the
// previous byte has been put on the stream (so the reader trails the peer by
// about one frame).
func TestC7_EndToEndMessageAssembly(t *testing.T) {
	const numFrames = 4000
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	streamCh := make(chan *ServerStream, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		st, err := NewServerTransport(conn, &ServerConfig{MaxStreams: math.MaxUint32, BufferPool: mem.DefaultBufferPool()})
		if err != nil {
			return
		}
		defer st.Close(errors.New("test done"))
		st.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s })
	}()
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write(clientPreface)
	framer := http2.NewFramer(conn, conn)
	framer.WriteSettings()
	go func() {
		for {
			if _, err := framer.ReadFrame(); err != nil {
				return
			}
		}
	}()
	var hbuf bytes.Buffer
	henc := hpack.NewEncoder(&hbuf)
	for _, hf := range []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":path", Value: "/foo"}, {Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"}} {
		henc.WriteField(hf)
	}
	framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true})
	stream := <-streamCh

	want := make([]byte, numFrames)
	for i := range want {
		want[i] = byte(i%251 + 1)
	}
	var sent atomic.Int64
	go func() {
		for i := 0; i < numFrames; i++ {
			if err := framer.WriteData(1, false, want[i:i+1]); err != nil {
				return
			}
			sent.Add(1)
		}
	}()
	data, err := stream.Read(numFrames) // message assembly: retains every delivered buffer
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	distinct, sparse, arrays := c7Retained(data)
	got := data.Materialize()
	t.Logf("C7 e2e env=%q: %d-byte message in %d one-byte DATA frames; inOrder=%v; buffers in assembled message=%d; distinct backing arrays=%d; >=16KiB arrays holding <=4 payload bytes=%d; distinct retained capacity=%d bytes (%.0fx payload)",
		os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), numFrames, numFrames, bytes.Equal(got, want), len(data), arrays, sparse, distinct, float64(distinct)/float64(numFrames))
	data.Free()
}

// Message assembly through the real Stream.read loop: the reader is blocked
// in Stream.read(N) on a newStream-constructed client stream. The next
// one-byte payload arrives while the reader accounts for the buffer it just
// consumed (delivered from the window-update hook), so exactly one message is
// always queued behind the reader.
func TestC7_StreamReadAssembly(t *testing.T) {
	const n = 1024
	pool := mem.DefaultBufferPool()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := (&http2Client{bufferPool: pool}).newStream(ctx, &CallHdr{}, nil)
	want := make([]byte, n)
	for i := range want {
		want[i] = byte(i%251 + 1)
	}
	next := 0
	putNext := func() {
		if next < n {
			s.buf.put(recvMsg{buffer: mem.Copy(want[next:next+1], pool)})
			next++
		}
	}
	s.readRequester = &fakeReadRequester{}
	s.trReader = transportReader{
		reader:        recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &s.buf},
		windowHandler: &mockWindowUpdater{f: func(int) { putNext() }},
	}
	putNext()
	putNext()
	data, err := s.read(n)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	distinct, sparse, arrays := c7Retained(data)
	t.Logf("C7 Stream.read env=%q: %d-byte message in %d one-byte payloads; inOrder=%v; buffers in assembled message=%d; distinct backing arrays=%d; >=16KiB arrays holding <=4 payload bytes=%d; distinct retained capacity=%d bytes (%.0fx payload)",
		os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), n, n, bytes.Equal(data.Materialize(), want), len(data), arrays, sparse, distinct, float64(distinct)/float64(n))
	data.Free()
}
