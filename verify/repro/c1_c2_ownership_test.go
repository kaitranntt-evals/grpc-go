// Run: git worktree add --detach /tmp/wt-34099504 <claims-remote>/evalon/grpc-go-tr-34099504 && cp verify/repro/c1_c2_ownership_test.go /tmp/wt-34099504/internal/transport/verify_c1_c2_ownership_test.go && cp <eval_tests.zip>/tests/eval_recv_buffer_compaction_test.go /tmp/wt-34099504/internal/transport/ && cd /tmp/wt-34099504 && go test -tags verify_repro -v -run '^TestVerify_C[12]_' ./internal/transport -race -count=1

//go:build verify_repro

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/mem"
)

// verifyTracePool wraps a pool and records, per outstanding allocation, its
// capacity and the call stack that acquired it.
type verifyTracePool struct {
	inner mem.BufferPool
	mu    sync.Mutex
	out   map[uintptr]string
	gets  int
	puts  int
}

func newVerifyTracePool(t *testing.T) *verifyTracePool {
	inner, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
	if err != nil {
		t.Fatal(err)
	}
	return &verifyTracePool{inner: inner, out: map[uintptr]string{}}
}

func verifyCaller() string {
	pcs := make([]uintptr, 8)
	n := runtime.Callers(3, pcs)
	fr := runtime.CallersFrames(pcs[:n])
	var names []string
	for {
		f, more := fr.Next()
		names = append(names, f.Function[strings.LastIndex(f.Function, "/")+1:])
		if !more || len(names) == 4 {
			break
		}
	}
	return strings.Join(names, " <- ")
}

func (p *verifyTracePool) Get(n int) *[]byte {
	b := p.inner.Get(n)
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	p.gets++
	p.out[ptr] = fmt.Sprintf("cap=%d acquired by %s", cap(*b), verifyCaller())
	p.mu.Unlock()
	return b
}

func (p *verifyTracePool) Put(b *[]byte) {
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	p.puts++
	delete(p.out, ptr)
	p.mu.Unlock()
	p.inner.Put(b)
}

func (p *verifyTracePool) snapshot() (gets, puts int, out map[uintptr]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out = map[uintptr]string{}
	for k, v := range p.out {
		out[k] = v
	}
	return p.gets, p.puts, out
}

// verifyOwner reports whether the recvBuffer's live compaction buffer is backed
// by the allocation at ptr.
func verifyOwner(b *recvBuffer) (owner uintptr, live bool, start, end int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.compactBuf == nil || len(b.compactData) == 0 {
		return 0, false, b.compactStart, b.compactEnd
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(b.compactData))), true, b.compactStart, b.compactEnd
}

func verifyReport(t *testing.T, label string, p *verifyTracePool, b *recvBuffer) (outstanding int, ownedByRecvBuffer int) {
	t.Helper()
	gets, puts, out := p.snapshot()
	owner, live, start, end := verifyOwner(b)
	t.Logf("%s: pool gets=%d puts=%d outstanding=%d | recvBuffer.compactBuf live=%v pending=[%d,%d) err=%v", label, gets, puts, len(out), live, start, end, b.err)
	for ptr, desc := range out {
		owned := live && ptr == owner
		if owned {
			ownedByRecvBuffer++
		}
		t.Logf("%s:   outstanding %#x %s | backs recvBuffer.compactBuf=%v", label, ptr, desc, owned)
	}
	return len(out), ownedByRecvBuffer
}

// C1: client stream, n one-byte payloads (n=1026 is the fixture's
// UnpooledConsolidation input), drained and freed with the stream kept open.
func verifyC1Run(t *testing.T, n int) {
	pool := newVerifyTracePool(t)
	client := evalNewProductionHTTP2Client(t, pool)
	s := client.newStream(context.Background(), &CallHdr{}, nil)

	drain := func(want int) []byte {
		var got []byte
		deadline := time.Now().Add(5 * time.Second)
		for len(got) < want {
			if time.Now().After(deadline) {
				t.Fatalf("drain timeout: %d/%d", len(got), want)
			}
			s.buf.load()
			select {
			case m := <-s.buf.c:
				got = append(got, m.buffer.ReadOnlyData()...)
				m.buffer.Free()
			default:
			}
		}
		return got
	}

	var want []byte
	for i := range n {
		p := []byte{byte((i*7)%251 + 1)}
		want = append(want, p...)
		s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
	}
	if got := drain(len(want)); !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	t.Logf("n=%d: stream state after drain: %v (streamActive=%v), recvBuffer.err=%v", n, s.getState(), streamActive, s.buf.err)
	out1, owned1 := verifyReport(t, "after drain+free, stream open", pool, &s.buf)
	owner1, _, _, _ := verifyOwner(&s.buf)
	gets1, puts1, _ := pool.snapshot()

	// Subsequent input on the still-open stream.
	var want2 []byte
	for i := range 500 {
		p := []byte{byte(i%200 + 1)}
		want2 = append(want2, p...)
		s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
	}
	owner2, _, _, _ := verifyOwner(&s.buf)
	gets2, puts2, _ := pool.snapshot()
	t.Logf("subsequent 500 one-byte payloads: new pool gets=%d puts=%d, same compaction destination=%v", gets2-gets1, puts2-puts1, owner1 == owner2)
	if got := drain(len(want2)); !bytes.Equal(got, want2) {
		t.Fatalf("payload mismatch on second burst")
	}
	out2, owned2 := verifyReport(t, "after second drain+free, stream open", pool, &s.buf)

	s.buf.put(recvMsg{err: io.EOF})
	s.buf.load()
	if m := <-s.buf.c; m.err != io.EOF {
		t.Fatalf("want EOF, got %v", m)
	}
	out3, _ := verifyReport(t, "after stream termination (EOF)", pool, &s.buf)

	t.Logf("RESULT C1 n=%d: after drain outstanding=%d ownedByLiveRecvBuffer=%d orphaned=%d; after 2nd burst outstanding=%d owned=%d orphaned=%d; outstanding after termination=%d",
		n, out1, owned1, out1-owned1, out2, owned2, out2-owned2, out3)
}

func TestVerify_C1_DrainedOpenStreamOwnership(t *testing.T) {
	// 1026: fixture input. 16385: fills the 16KiB destination exactly.
	// 50001: rolls over several destinations.
	for _, n := range []int{evalFragmentThreshold + 2, 16385, 50001} {
		t.Run(fmt.Sprint(n), func(t *testing.T) { verifyC1Run(t, n) })
	}
}

type verifyServerFixture struct {
	pool   *verifyTracePool
	st     *http2Server
	stream *ServerStream
	framer *http2.Framer
	conn   net.Conn
	pings  chan [8]byte
	wmu    sync.Mutex
}

func verifyNewServerStream(t *testing.T) *verifyServerFixture {
	t.Helper()
	f := &verifyServerFixture{pool: newVerifyTracePool(t), pings: make(chan [8]byte, 16)}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })
	streamCh := make(chan *ServerStream, 1)
	stCh := make(chan *http2Server, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		tr, err := NewServerTransport(conn, &ServerConfig{BufferPool: f.pool, MaxStreams: 100, KeepalivePolicy: keepalive.EnforcementPolicy{MinTime: time.Millisecond, PermitWithoutStream: true}})
		if err != nil {
			t.Errorf("NewServerTransport: %v", err)
			return
		}
		stCh <- tr.(*http2Server)
		tr.HandleStreams(context.Background(), func(s *ServerStream) { streamCh <- s })
	}()
	f.conn, err = net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.conn.Close() })
	if _, err := f.conn.Write(clientPreface); err != nil {
		t.Fatal(err)
	}
	f.framer = http2.NewFramer(f.conn, f.conn)
	if err := f.framer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			fr, err := f.framer.ReadFrame()
			if err != nil {
				return
			}
			switch fr := fr.(type) {
			case *http2.SettingsFrame:
				if !fr.IsAck() {
					f.wmu.Lock()
					f.framer.WriteSettingsAck()
					f.wmu.Unlock()
				}
			case *http2.PingFrame:
				if fr.IsAck() {
					f.pings <- fr.Data
				} else {
					f.wmu.Lock()
					f.framer.WritePing(true, fr.Data)
					f.wmu.Unlock()
				}
			}
		}
	}()
	var hb bytes.Buffer
	henc := hpack.NewEncoder(&hb)
	for _, hf := range []hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":path", Value: "/svc/m"}, {Name: ":authority", Value: "localhost"}, {Name: ":scheme", Value: "http"}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"}} {
		henc.WriteField(hf)
	}
	f.wmu.Lock()
	err = f.framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true})
	f.wmu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case f.st = <-stCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no server transport")
	}
	t.Cleanup(func() { f.st.Close(fmt.Errorf("test done")) })
	select {
	case f.stream = <-streamCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no server stream")
	}
	return f
}

func (f *verifyServerFixture) write(fn func() error) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	return fn()
}

// sync sends a PING and waits for its ack: the server reader handles frames in
// order, so every frame written before the PING has been processed.
func (f *verifyServerFixture) sync(t *testing.T, id byte) {
	t.Helper()
	data := [8]byte{'v', 'e', 'r', 'i', 'f', 'y', 0, id}
	if err := f.write(func() error { return f.framer.WritePing(false, data) }); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case got := <-f.pings:
			if got == data {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no PING ack")
		}
	}
}

func verifyC2Run(t *testing.T, terminate string) (afterReads, afterReadsOwned, afterTerm, afterClose int) {
	f := verifyNewServerStream(t)
	s := f.stream
	const n = 1026
	var want []byte
	for i := range n {
		p := []byte{byte((i*7)%251 + 1)}
		want = append(want, p...)
		if err := f.write(func() error { return f.framer.WriteData(1, false, p) }); err != nil {
			t.Fatal(err)
		}
	}
	f.sync(t, 1)
	verifyReport(t, "all DATA received, nothing read", f.pool, &s.buf)

	var got []byte
	for len(got) < n {
		bs, err := s.Read(1)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		got = append(got, bs.Materialize()...)
		bs.Free()
	}
	if !bytes.Equal(got, want) {
		t.Fatal("payload mismatch")
	}
	afterReads, afterReadsOwned = verifyReport(t, "after reading+freeing all delivered buffers", f.pool, &s.buf)

	switch terminate {
	case "rst":
		// Client cancels: RST_STREAM(CANCEL), no END_STREAM.
		if err := f.write(func() error { return f.framer.WriteRSTStream(1, http2.ErrCodeCancel) }); err != nil {
			t.Fatal(err)
		}
	case "eof":
		if err := f.write(func() error { return f.framer.WriteData(1, true, nil) }); err != nil {
			t.Fatal(err)
		}
	}
	f.sync(t, 2)
	if terminate == "rst" {
		select {
		case <-s.Context().Done():
		case <-time.After(5 * time.Second):
			t.Fatal("stream ctx not canceled")
		}
		f.st.mu.Lock()
		_, active := f.st.activeStreams[1]
		f.st.mu.Unlock()
		t.Logf("after RST_STREAM: ctx.Err=%v state==streamDone:%v inActiveStreams=%v recvBuffer.err=%v", s.Context().Err(), s.getState() == streamDone, active, s.buf.err)
		_, rerr := s.Read(1)
		t.Logf("application Read after cancellation returns: %v", rerr)
	} else {
		_, rerr := s.Read(1)
		t.Logf("application Read after END_STREAM returns: %v", rerr)
	}
	time.Sleep(200 * time.Millisecond)
	afterTerm, _ = verifyReport(t, "after "+terminate+" termination", f.pool, &s.buf)

	f.st.Close(fmt.Errorf("closing"))
	time.Sleep(200 * time.Millisecond)
	afterClose, _ = verifyReport(t, "after server transport Close", f.pool, &s.buf)
	return
}

// C2: server stream, compaction exercised, drained+freed, then client cancel
// (RST_STREAM) with no END_STREAM.
func TestVerify_C2_ServerCancelWithoutEOF(t *testing.T) {
	a, owned, b, c := verifyC2Run(t, "rst")
	t.Logf("RESULT C2 (RST_STREAM, no EOF): outstanding afterReads=%d (owned by recvBuffer.compactBuf=%d) afterCancel=%d afterTransportClose=%d", a, owned, b, c)
	if c > 0 {
		t.Errorf("%d compaction destination(s) acquired from the configured pool were never returned after cancellation", c)
	}
}

// Control: the same sequence terminated by END_STREAM instead of cancellation.
func TestVerify_C2_ControlEOF(t *testing.T) {
	a, owned, b, c := verifyC2Run(t, "eof")
	t.Logf("RESULT control (END_STREAM): outstanding afterReads=%d (owned by recvBuffer.compactBuf=%d) afterEOF=%d afterTransportClose=%d", a, owned, b, c)
}
