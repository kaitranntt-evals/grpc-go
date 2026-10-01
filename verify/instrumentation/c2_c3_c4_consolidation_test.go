// Run (from the root of a grpc-go checkout of this verify branch): git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-2d69d401 && git worktree add --detach /tmp/wt-2d69d401 FETCH_HEAD && cp verify/instrumentation/c2_c3_c4_consolidation_test.go /tmp/wt-2d69d401/internal/transport/zz_verify_c2_c3_c4_test.go && (cd /tmp/wt-2d69d401 && go test ./internal/transport -run 'TestVerifyC[234]' -count=1 -v)

package transport

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"google.golang.org/grpc/mem"
)

// vQueued returns (#backlog entries, 1 if an open tail exists, payload bytes
// queued in backlog+tail, backing capacity held by backlog+tail).
func vQueued(b *recvBuffer) (entries, tail, payload, capacity int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.backlog {
		if m.buffer != nil {
			payload += m.buffer.Len()
			capacity += cap(m.buffer.ReadOnlyData())
		}
	}
	if b.tail != nil {
		tail = 1
		payload += b.tailLen
		capacity += cap(*b.tail)
	}
	return len(b.backlog), tail, payload, capacity
}

// vCountingPool wraps a pool and counts outstanding Gets.
type vCountingPool struct {
	mu          sync.Mutex
	inner       mem.BufferPool
	gets, puts  int
	outstanding map[*[]byte]int
}

func newVCountingPool() *vCountingPool {
	return &vCountingPool{inner: mem.DefaultBufferPool(), outstanding: map[*[]byte]int{}}
}
func (p *vCountingPool) Get(n int) *[]byte {
	b := p.inner.Get(n)
	p.mu.Lock()
	p.gets++
	p.outstanding[b] = cap(*b)
	p.mu.Unlock()
	return b
}
func (p *vCountingPool) Put(b *[]byte) {
	p.mu.Lock()
	p.puts++
	delete(p.outstanding, b)
	p.mu.Unlock()
	p.inner.Put(b)
}
func (p *vCountingPool) stats() (gets, puts, outstanding, outstandingBytes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.outstanding {
		outstandingBytes += c
	}
	return p.gets, p.puts, len(p.outstanding), outstandingBytes
}

func vDrain(t *testing.T, b *recvBuffer, want int) []byte {
	t.Helper()
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out draining: got %d of %d bytes", len(got), want)
		}
		select {
		case m := <-b.get():
			if m.buffer != nil {
				got = append(got, m.buffer.ReadOnlyData()...)
				m.buffer.Free()
			}
			b.load()
		default:
			b.load()
		}
	}
	return got
}

// ---------- C2: sustained tiny-frame backlog, reader delayed ----------

// Unit level: recvBuffer initialized exactly the way http2_client.go /
// http2_server.go / handler_server.go initialize it on this branch.
func TestVerifyC2_Unit_SustainedTinyBacklog(t *testing.T) {
	for _, mode := range []string{"initWithPool(pool) [what the transports call]", "init() [what the eval fixture's helper falls through to]"} {
		b := &recvBuffer{}
		if mode[:5] == "initW" {
			b.initWithPool(mem.DefaultBufferPool())
		} else {
			b.init()
		}
		var want []byte
		for i := 1; i <= 20000; i++ {
			v := byte(i%251 + 1)
			want = append(want, v)
			b.put(recvMsg{buffer: mem.Copy([]byte{v}, mem.DefaultBufferPool())})
			switch i {
			case 2, 16, 64, 512, 1024, 1026, 2048, 4096, 8192, 16384, 20000:
				e, tl, p, c := vQueued(b)
				t.Logf("C2 %-58s frames=%-6d backlog_entries=%-6d open_tail=%d queued_payload=%-6d backing_cap=%d", mode, i, e, tl, p, c)
			}
		}
		if got := vDrain(t, b, len(want)); !bytes.Equal(got, want) {
			t.Fatalf("%s: payload mismatch", mode)
		}
	}
}

// ---------- shared raw-HTTP/2 client against a real http2Server ----------

type vRawClient struct {
	t      *testing.T
	conn   net.Conn
	mu     sync.Mutex
	framer *http2.Framer
	acks   chan [8]byte
	done   chan struct{}
}

func newVRawClient(t *testing.T, addr string) *vRawClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write(clientPreface); err != nil {
		t.Fatalf("preface: %v", err)
	}
	c := &vRawClient{t: t, conn: conn, framer: http2.NewFramer(conn, conn), acks: make(chan [8]byte, 64), done: make(chan struct{})}
	if err := c.framer.WriteSettings(); err != nil {
		t.Fatalf("settings: %v", err)
	}
	go func() {
		defer close(c.done)
		for {
			f, err := c.framer.ReadFrame()
			if err != nil {
				return
			}
			if pf, ok := f.(*http2.PingFrame); ok {
				if pf.IsAck() {
					c.acks <- pf.Data
				} else {
					c.mu.Lock()
					c.framer.WritePing(true, pf.Data)
					c.mu.Unlock()
				}
			}
		}
	}()
	var hbuf bytes.Buffer
	henc := hpack.NewEncoder(&hbuf)
	for _, hf := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"}, {Name: ":path", Value: "foo"},
		{Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"},
	} {
		henc.WriteField(hf)
	}
	c.mu.Lock()
	err = c.framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true})
	c.mu.Unlock()
	if err != nil {
		t.Fatalf("headers: %v", err)
	}
	return c
}

func (c *vRawClient) data(p []byte) {
	c.mu.Lock()
	err := c.framer.WriteData(1, false, p)
	c.mu.Unlock()
	if err != nil {
		c.t.Fatalf("WriteData: %v", err)
	}
}

// waitPending blocks until the server-side stream has received exactly
// `want` not-yet-read payload bytes (fc.pendingData is updated by the
// transport's reader goroutine just before it calls recvBuffer.put).
func vWaitPending(t *testing.T, ss *ServerStream, want int) {
	t.Helper()
	waitWhileTrue(t, func() (bool, error) {
		ss.fc.mu.Lock()
		got := int(ss.fc.pendingData)
		ss.fc.mu.Unlock()
		if got != want {
			return true, fmt.Errorf("pendingData=%d want %d", got, want)
		}
		// pendingData is bumped immediately before put(); give put() a moment.
		time.Sleep(20 * time.Millisecond)
		return false, nil
	})
}

func vServerStream(t *testing.T, server *server) *ServerStream {
	t.Helper()
	var sstream *ServerStream
	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		for k := range server.conns {
			st := k.(*http2Server)
			st.mu.Lock()
			for _, v := range st.activeStreams {
				if v.id == 1 {
					sstream = v
				}
			}
			st.mu.Unlock()
		}
		if sstream == nil {
			return true, fmt.Errorf("stream not created")
		}
		return false, nil
	})
	return sstream
}

// End to end: a real http2Server (default buffer pool, as grpc.NewServer
// configures it) receives one-byte DATA frames on a stream nobody reads.
func TestVerifyC2_E2E_SustainedTinyBacklog(t *testing.T) {
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)
	defer server.stop()
	c := newVRawClient(t, server.lis.Addr().String())
	defer c.conn.Close()
	ss := vServerStream(t, server)

	var want []byte
	sent := 0
	for _, upTo := range []int{2, 64, 512, 1024, 1026, 2048, 4096, 8192, 16384, 30000} {
		for sent < upTo {
			v := byte(sent%251 + 1)
			want = append(want, v)
			c.data([]byte{v})
			sent++
		}
		vWaitPending(t, ss, sent)
		e, tl, p, cp := vQueued(&ss.buf)
		t.Logf("C2 e2e http2Server: DATA frames received=%-6d backlog_entries=%-5d open_tail=%d queued_payload=%-6d backing_cap=%d", sent, e, tl, p, cp)
	}
	got := make([]byte, len(want))
	if _, err := ss.readTo(got); err != nil {
		t.Fatalf("readTo: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	t.Logf("C2 e2e: all %d bytes delivered in order", len(got))
}

// ---------- C3: mixed-size frames ----------

func vMixedSeq() (frames [][]byte, tiny, large int) {
	// 5,000 tiny frames (1..7 bytes) with an 8 KiB frame after every 250th
	// tiny frame and a 16 KiB frame after every 1,000th.
	for i := 0; i < 5000; i++ {
		p := make([]byte, i%7+1)
		for j := range p {
			p[j] = byte((i*13+j)%251 + 1)
		}
		frames = append(frames, p)
		tiny++
		if (i+1)%1000 == 0 {
			frames = append(frames, bytes.Repeat([]byte{byte(i%200 + 1)}, 16384))
			large++
		} else if (i+1)%250 == 0 {
			frames = append(frames, bytes.Repeat([]byte{byte(i%200 + 1)}, 8192))
			large++
		}
	}
	return
}

func TestVerifyC3_Unit_MixedFrames(t *testing.T) {
	pool := mem.DefaultBufferPool()
	// (a) the eval fixture's own sequence: 1,074 frames of 1..7 bytes.
	{
		b := &recvBuffer{}
		b.initWithPool(pool)
		var want []byte
		for i := 0; i < 1074; i++ {
			p := make([]byte, i%7+1)
			for j := range p {
				p[j] = byte((i*13+j)%251 + 1)
			}
			want = append(want, p...)
			b.put(recvMsg{buffer: mem.Copy(p, pool)})
		}
		e, tl, p, c := vQueued(b)
		t.Logf("C3 fixture sequence (1074 frames of 1..7 bytes) via initWithPool: backlog_entries=%d open_tail=%d queued_payload=%d backing_cap=%d (fixture bound: <=134)", e, tl, p, c)
		if got := vDrain(t, b, len(want)); !bytes.Equal(got, want) {
			t.Fatalf("payload mismatch")
		}
	}
	// (b) thousands of tiny frames with intervening larger frames.
	frames, tiny, large := vMixedSeq()
	b := &recvBuffer{}
	b.initWithPool(pool)
	var want []byte
	for i, p := range frames {
		want = append(want, p...)
		b.put(recvMsg{buffer: mem.Copy(p, pool)})
		if n := i + 1; n == 252 || n == 1005 || n == 2510 || n == len(frames) {
			e, tl, pl, c := vQueued(b)
			t.Logf("C3 mixed sequence via initWithPool: frames_put=%-5d backlog_entries=%-4d open_tail=%d queued_payload=%-7d backing_cap=%d", n, e, tl, pl, c)
		}
	}
	// Classify what is queued.
	b.mu.Lock()
	var largeIntact, consolidated, tinySingles int
	for _, m := range b.backlog {
		switch l := m.buffer.Len(); {
		case l >= 8192 && bytes.Count(m.buffer.ReadOnlyData(), m.buffer.ReadOnlyData()[:1]) == l:
			largeIntact++
		case l <= 7:
			tinySingles++
		default:
			consolidated++
		}
	}
	b.mu.Unlock()
	t.Logf("C3 mixed sequence: sent tiny=%d large=%d -> queued entries: large-intact=%d consolidated-chunks=%d un-consolidated-tiny=%d", tiny, large, largeIntact, consolidated, tinySingles)
	if got := vDrain(t, b, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	t.Logf("C3 mixed sequence: all %d bytes delivered in order", len(want))
}

func TestVerifyC3_E2E_MixedFrames(t *testing.T) {
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)
	defer server.stop()
	c := newVRawClient(t, server.lis.Addr().String())
	defer c.conn.Close()
	ss := vServerStream(t, server)

	// 4,000 tiny frames (1..7 bytes) with a 4,500-byte frame after every
	// 500th: 16,000 + 8*4,500 = 52,000 bytes, inside the 64 KiB window.
	var want []byte
	tiny, large := 0, 0
	for i := 0; i < 4000; i++ {
		p := make([]byte, i%7+1)
		for j := range p {
			p[j] = byte((i*13+j)%251 + 1)
		}
		want = append(want, p...)
		c.data(p)
		tiny++
		if (i+1)%500 == 0 {
			lp := bytes.Repeat([]byte{byte(large + 1)}, 4500)
			want = append(want, lp...)
			c.data(lp)
			large++
			vWaitPending(t, ss, len(want))
			e, tl, p, cp := vQueued(&ss.buf)
			t.Logf("C3 e2e http2Server: tiny_frames=%-5d large_frames=%d backlog_entries=%-4d open_tail=%d queued_payload=%-6d backing_cap=%d", tiny, large, e, tl, p, cp)
		}
	}
	got := make([]byte, len(want))
	if _, err := ss.readTo(got); err != nil {
		t.Fatalf("readTo: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	t.Logf("C3 e2e: all %d bytes delivered in order", len(got))
}

// ---------- C4: repeated receive-and-read cycles ----------

func TestVerifyC4_Unit_Cycles(t *testing.T) {
	pool := newVCountingPool()
	b := &recvBuffer{}
	b.initWithPool(pool)
	const cycles, perCycle = 8, 1124
	for cy := 0; cy < cycles; cy++ {
		var want []byte
		for i := 0; i < perCycle; i++ {
			p := make([]byte, i%5+1)
			for j := range p {
				p[j] = byte((cy*31+i*7+j)%251 + 1)
			}
			want = append(want, p...)
			b.put(recvMsg{buffer: mem.Copy(p, pool)})
		}
		e, tl, pl, c := vQueued(b)
		_, _, o, ob := pool.stats()
		t.Logf("C4 cycle %d peak : frames=%d backlog_entries=%d open_tail=%d queued_payload=%d backing_cap=%d pool_outstanding=%d (%d bytes)", cy, perCycle, e, tl, pl, c, o, ob)
		if got := vDrain(t, b, len(want)); !bytes.Equal(got, want) {
			t.Fatalf("cycle %d payload mismatch", cy)
		}
		e, tl, pl, c = vQueued(b)
		g, p, o, ob := pool.stats()
		b.mu.Lock()
		bc := cap(b.backlog)
		b.mu.Unlock()
		t.Logf("C4 cycle %d drain: backlog_entries=%d open_tail=%d queued_payload=%d backing_cap=%d cap(backlog)=%d pool gets=%d puts=%d outstanding=%d (%d bytes)", cy, e, tl, pl, c, bc, g, p, o, ob)
	}
}

func TestVerifyC4_E2E_Cycles(t *testing.T) {
	pool := newVCountingPool()
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: pool}, suspended)
	defer server.stop()
	c := newVRawClient(t, server.lis.Addr().String())
	defer c.conn.Close()
	ss := vServerStream(t, server)

	const cycles, perCycle = 6, 3000
	for cy := 0; cy < cycles; cy++ {
		var want []byte
		for i := 0; i < perCycle; i++ {
			p := make([]byte, i%5+1)
			for j := range p {
				p[j] = byte((cy*31+i*7+j)%251 + 1)
			}
			want = append(want, p...)
			c.data(p)
		}
		vWaitPending(t, ss, len(want))
		e, tl, pl, cp := vQueued(&ss.buf)
		_, _, o, ob := pool.stats()
		t.Logf("C4 e2e cycle %d peak : DATA frames=%d backlog_entries=%d open_tail=%d queued_payload=%d backing_cap=%d pool_outstanding=%d (%d bytes)", cy, perCycle, e, tl, pl, cp, o, ob)
		got := make([]byte, len(want))
		if _, err := ss.readTo(got); err != nil {
			t.Fatalf("readTo: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("cycle %d payload mismatch", cy)
		}
		vWaitPending(t, ss, 0)
		e, tl, pl, cp = vQueued(&ss.buf)
		g, p, o, ob := pool.stats()
		t.Logf("C4 e2e cycle %d drain: backlog_entries=%d open_tail=%d queued_payload=%d backing_cap=%d pool gets=%d puts=%d outstanding=%d (%d bytes)", cy, e, tl, pl, cp, g, p, o, ob)
	}
	_ = io.EOF
}
