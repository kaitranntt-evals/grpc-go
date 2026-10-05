// Run: verify/repro/run_probe.sh <worktree-of-branch-under-test> 'TestVerifyProbe'   (copies this file into internal/transport, runs it, removes it)

package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

// ---------------------------------------------------------------------------
// Tracing pool: records who acquired every buffer and what is still out.
// ---------------------------------------------------------------------------

type probeRec struct {
	req, capacity int
	base          uintptr
	by            string // innermost internal/transport function on the stack
	recvBuf       bool   // stack contains a recvBuffer method
}

type probePool struct {
	inner mem.BufferPool
	mu    sync.Mutex
	out   map[uintptr]probeRec // outstanding, keyed by backing array base
	last  map[uintptr]probeRec // most recent Get per backing array (even if returned)
	gets  []probeRec
	puts  int
}

func newProbePool(inner mem.BufferPool) *probePool {
	return &probePool{inner: inner, out: map[uintptr]probeRec{}, last: map[uintptr]probeRec{}}
}

func probeCaller() (string, bool) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	by, recv := "", false
	for {
		f, more := frames.Next()
		const pfx = "google.golang.org/grpc/internal/transport."
		if strings.HasPrefix(f.Function, pfx) && !strings.Contains(f.File, "zz_verify_probe_test.go") {
			name := strings.TrimPrefix(f.Function, pfx)
			if by == "" {
				by = name
			}
			if strings.Contains(name, "recvBuffer") {
				recv = true
			}
		}
		if !more {
			break
		}
	}
	if by == "" {
		by = "(outside internal/transport)"
	}
	return by, recv
}

func (p *probePool) Get(n int) *[]byte {
	b := p.inner.Get(n)
	by, recv := probeCaller()
	r := probeRec{req: n, capacity: cap(*b), base: uintptr(unsafe.Pointer(unsafe.SliceData((*b)[:cap(*b)]))), by: by, recvBuf: recv}
	p.mu.Lock()
	p.out[r.base] = r
	p.last[r.base] = r
	p.gets = append(p.gets, r)
	p.mu.Unlock()
	return b
}

func (p *probePool) Put(b *[]byte) {
	base := uintptr(unsafe.Pointer(unsafe.SliceData((*b)[:cap(*b)])))
	p.mu.Lock()
	delete(p.out, base)
	p.puts++
	p.mu.Unlock()
	p.inner.Put(b)
}

// summary returns "caller(req sizes) xN" lines for Gets since index from.
func (p *probePool) getsSince(from int) (int, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	type k struct {
		by       string
		capacity int
	}
	m := map[k]int{}
	for _, r := range p.gets[from:] {
		m[k{r.by, r.capacity}]++
	}
	var lines []string
	for key, c := range m {
		lines = append(lines, fmt.Sprintf("%s cap=%d x%d", key.by, key.capacity, c))
	}
	sort.Strings(lines)
	return len(p.gets) - from, strings.Join(lines, "; ")
}

func (p *probePool) numGets() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.gets)
}

func (p *probePool) outstanding(onlyRecvBuf bool) (int, int, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, capSum := 0, 0
	m := map[string]int{}
	for _, r := range p.out {
		if onlyRecvBuf && !r.recvBuf {
			continue
		}
		n++
		capSum += r.capacity
		m[fmt.Sprintf("%s cap=%d", r.by, r.capacity)]++
	}
	var lines []string
	for k, c := range m {
		lines = append(lines, fmt.Sprintf("%s x%d", k, c))
	}
	sort.Strings(lines)
	return n, capSum, strings.Join(lines, "; ")
}

// owner returns the record of the pool buffer whose backing array contains d.
func (p *probePool) owner(d []byte) (probeRec, bool) {
	if len(d) == 0 {
		return probeRec{}, false
	}
	ptr := uintptr(unsafe.Pointer(&d[0]))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.last {
		if ptr >= r.base && ptr < r.base+uintptr(r.capacity) {
			return r, true
		}
	}
	return probeRec{}, false
}

// ---------------------------------------------------------------------------
// Real server transport + raw HTTP/2 client.
// ---------------------------------------------------------------------------

type probeServer struct {
	lis      net.Listener
	streamCh chan *ServerStream
	stCh     chan ServerTransport
}

func startProbeServer(t *testing.T, ctx context.Context, pool mem.BufferPool) *probeServer {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	ps := &probeServer{lis: lis, streamCh: make(chan *ServerStream, 16), stCh: make(chan ServerTransport, 1)}
	t.Cleanup(func() { lis.Close() })
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		st, err := NewServerTransport(conn, &ServerConfig{MaxStreams: math.MaxUint32, BufferPool: pool})
		if err != nil {
			conn.Close()
			return
		}
		ps.stCh <- st
		st.HandleStreams(ctx, func(s *ServerStream) { ps.streamCh <- s })
	}()
	return ps
}

type probeClient struct {
	conn net.Conn
	fr   *http2.Framer
}

func dialProbeClient(t *testing.T, addr string) *probeClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil {
		t.Fatal(err)
	}
	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	go func() { // discard everything the server sends
		for {
			if _, err := fr.ReadFrame(); err != nil {
				return
			}
		}
	}()
	return &probeClient{conn: conn, fr: fr}
}

func (c *probeClient) headers(t *testing.T, id uint32) {
	t.Helper()
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, f := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "http"}, {Name: ":path", Value: "/svc/m"},
		{Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"},
	} {
		enc.WriteField(f)
	}
	if err := c.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: hb.Bytes(), EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
}

func probeWaitStream(t *testing.T, ps *probeServer) *ServerStream {
	t.Helper()
	select {
	case s := <-ps.streamCh:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for server stream")
	}
	return nil
}

// probeQueued returns the queued data entries (channel entry first), waiting
// until wantBytes payload bytes are queued.
func probeQueued(t *testing.T, b *recvBuffer, wantBytes int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var out [][]byte
		total := 0
		b.mu.Lock()
		select {
		case m := <-b.c:
			if m.buffer != nil {
				out = append(out, m.buffer.ReadOnlyData())
				total += m.buffer.Len()
			}
			b.c <- m
		default:
		}
		for _, m := range b.backlog {
			if m.buffer != nil {
				out = append(out, m.buffer.ReadOnlyData())
				total += m.buffer.Len()
			}
		}
		pending := probePendingBytes(b)
		b.mu.Unlock()
		if total+pending >= wantBytes {
			if pending > 0 {
				t.Logf("  (+%d bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)", pending)
			}
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %d of %d bytes queued", total+pending, wantBytes)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// probePendingBytes reports bytes held in branch-specific "open" compaction
// state that is not yet a backlog entry (looked up by reflection so the same
// probe compiles on every branch).
func probePendingBytes(b *recvBuffer) int {
	v := reflect.ValueOf(b).Elem()
	if f := v.FieldByName("compact"); f.IsValid() && f.Kind() == reflect.Ptr && !f.IsNil() { // 51f38834: *[]byte
		return f.Elem().Len()
	}
	if f := v.FieldByName("compacted"); f.IsValid() && f.Kind() == reflect.Ptr && !f.IsNil() { // db66491b: *[]byte
		return f.Elem().Len()
	}
	ce, ps := v.FieldByName("chunkEnd"), v.FieldByName("pendingStart") // 5401c189
	if ce.IsValid() && ps.IsValid() {
		return int(ce.Int() - ps.Int())
	}
	return 0
}

func probeFieldState(b *recvBuffer) string {
	v := reflect.ValueOf(b).Elem()
	var parts []string
	for _, name := range []string{"chunk", "compact", "compacted"} {
		if f := v.FieldByName(name); f.IsValid() && (f.Kind() == reflect.Ptr || f.Kind() == reflect.Interface) {
			parts = append(parts, fmt.Sprintf("recvBuffer.%s!=nil:%v", name, !f.IsNil()))
		}
	}
	if len(parts) == 0 {
		return "(no retained-compaction field on this branch)"
	}
	return strings.Join(parts, " ")
}

func probePattern(n, seed int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte((i+seed)%251 + 1)
	}
	return p
}

// ---------------------------------------------------------------------------
// C2: large DATA frames received by the real server transport while the
// handler is not reading: where does each queued entry's storage come from?
// ---------------------------------------------------------------------------

func TestVerifyProbe_C2_LargeFrames(t *testing.T) {
	for _, size := range []int{16384, 12000, 8193, 8192, 4096, 2048} {
		t.Run(fmt.Sprintf("frame=%d", size), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pool := newProbePool(mem.DefaultBufferPool())
			ps := startProbeServer(t, ctx, pool)
			c := dialProbeClient(t, ps.lis.Addr().String())
			c.headers(t, 1)
			ss := probeWaitStream(t, ps)

			var want []byte
			send := func(p []byte) {
				want = append(want, p...)
				if err := c.fr.WriteData(1, false, p); err != nil {
					t.Fatal(err)
				}
			}
			send([]byte("occupy")) // goes straight to the reader channel
			send([]byte{0x01})     // forces a non-empty queue
			const nLarge = 3
			for i := 0; i < nLarge; i++ {
				send(probePattern(size, i))
			}
			entries := probeQueued(t, &ss.buf, len(want))
			var got []byte
			orig, copied := 0, 0
			for i, d := range entries {
				got = append(got, d...)
				r, ok := pool.owner(d)
				src := "heap (not from pool)"
				if ok {
					src = fmt.Sprintf("pool buffer acquired by %s (Get(%d), cap=%d, entry starts at offset %d)", r.by, r.req, r.capacity, uintptr(unsafe.Pointer(&d[0]))-r.base)
				}
				t.Logf("  queue[%d]: len=%d -> %s", i, len(d), src)
				if ok && !r.recvBuf && len(d) == size && bytes.Equal(d, probePattern(size, orig)) {
					orig++ // this entry is exactly one large frame, still in the buffer the frame reader acquired
				}
			}
			copied = nLarge - orig
			n, sum := pool.getsSince(0)
			t.Logf("  pool.Get calls: %d [%s]", n, sum)
			t.Logf("RESULT frame=%d: large frames queued in their ORIGINAL frame-reader storage: %d of %d; copied into recvBuffer compaction storage: %d; %s; payload intact=%v",
				size, orig, nLarge, copied, probeFieldState(&ss.buf), bytes.HasPrefix(want, got))
		})
	}
}

// ---------------------------------------------------------------------------
// C3: tiny DATA frames on transport-created streams: is compaction storage
// acquired from the configured pool?
// ---------------------------------------------------------------------------

func TestVerifyProbe_C3_ServerStreamOverWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inner, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
	if err != nil {
		t.Fatal(err)
	}
	pool := newProbePool(inner)
	ps := startProbeServer(t, ctx, pool)
	c := dialProbeClient(t, ps.lis.Addr().String())
	c.headers(t, 1)
	ss := probeWaitStream(t, ps)
	before := pool.numGets()
	const n = 3000
	for i := 0; i < n; i++ {
		if err := c.fr.WriteData(1, false, []byte{byte(i%251 + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	entries := probeQueued(t, &ss.buf, n)
	fromPool, fromHeap := 0, 0
	byWho := map[string]int{}
	for _, d := range entries {
		if r, ok := pool.owner(d); ok {
			fromPool++
			byWho[r.by]++
		} else {
			fromHeap++
		}
	}
	gets, sum := pool.getsSince(before)
	t.Logf("server stream (NewServerTransport, configured pool traced), %d one-byte DATA frames, handler not reading:", n)
	t.Logf("  queue entries=%d ; backed by configured-pool buffers=%d %v ; backed by heap=%d", len(entries), fromPool, byWho, fromHeap)
	t.Logf("  configured pool Get calls while frames were queued: %d [%s]", gets, sum)
	rn, _, rsum := pool.outstanding(true)
	t.Logf("RESULT: pool acquisitions made from recvBuffer code and still held by the queue: %d [%s]", rn, rsum)
}

func probeDummyH2Server(t *testing.T) string {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(20 * time.Second))
		if err := http2.NewFramer(conn, conn).WriteSettings(); err != nil {
			return
		}
		io.Copy(io.Discard, conn)
	}()
	return lis.Addr().String()
}

func probeClientStreamPuts(t *testing.T, label string, client *http2Client, pool *probePool) {
	s := client.newStream(context.Background(), &CallHdr{}, nil)
	before := pool.numGets()
	const n = 1026
	var want []byte
	for i := 0; i < n; i++ {
		p := []byte{byte((i*7)%251 + 1)}
		want = append(want, p...)
		s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
	}
	gets, sum := pool.getsSince(before)
	s.buf.mu.Lock()
	backlog := len(s.buf.backlog)
	s.buf.mu.Unlock()
	t.Logf("%s: after %d one-byte puts: backlog=%d ; configured pool Get calls=%d [%s]", label, n, backlog, gets, sum)
	// drain, verify order
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < len(want) && time.Now().Before(deadline) {
		s.buf.load()
		select {
		case m := <-s.buf.c:
			if m.buffer != nil {
				got = append(got, m.buffer.ReadOnlyData()...)
				m.buffer.Free()
			}
		default:
		}
	}
	on, _, osum := pool.outstanding(true)
	t.Logf("%s: drained %d/%d bytes in order=%v ; recvBuffer-acquired pool buffers still outstanding after drain: %d [%s] ; %s",
		label, len(got), len(want), bytes.Equal(got, want), on, osum, probeFieldState(&s.buf))
	t.Logf("RESULT %s: compaction storage acquired from configured pool = %v", label, gets > 0)
}

func TestVerifyProbe_C3_ClientStream(t *testing.T) {
	mk := func() *probePool {
		inner, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
		if err != nil {
			t.Fatal(err)
		}
		return newProbePool(inner)
	}
	// (a) the delivered transport constructor
	pool := mk()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: probeDummyH2Server(t)}, ConnectOptions{BufferPool: pool}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatal(err)
	}
	defer ct.Close(errors.New("probe done"))
	probeClientStreamPuts(t, "client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool})", ct.(*http2Client), pool)

	// (b) how the eval fixture builds its client: a struct literal that skips the constructor
	pool2 := mk()
	probeClientStreamPuts(t, "client stream from fixture-style literal &http2Client{bufferPool: pool}", &http2Client{bufferPool: pool2}, pool2)
}

// ---------------------------------------------------------------------------
// C7: fully drained server stream, then RST_STREAM (or END_STREAM as control).
// ---------------------------------------------------------------------------

func TestVerifyProbe_C7_ResetAfterDrain(t *testing.T) {
	for _, end := range []string{"RST_STREAM", "END_STREAM(control)"} {
		t.Run(end, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pool := newProbePool(mem.DefaultBufferPool())
			ps := startProbeServer(t, ctx, pool)
			c := dialProbeClient(t, ps.lis.Addr().String())
			c.headers(t, 1)
			ss := probeWaitStream(t, ps)
			st := (<-ps.stCh).(*http2Server)

			const n = 100
			want := make([]byte, n)
			for i := range want {
				want[i] = byte(i + 1)
				if err := c.fr.WriteData(1, false, want[i:i+1]); err != nil {
					t.Fatal(err)
				}
			}
			probeQueued(t, &ss.buf, n)
			a, _, asum := pool.outstanding(true)
			t.Logf("1. %d one-byte frames queued, nothing read: recvBuffer-acquired pool buffers outstanding=%d [%s]", n, a, asum)

			got := make([]byte, n)
			if _, err := ss.readTo(got); err != nil { // readTo frees every delivered buffer
				t.Fatalf("readTo: %v", err)
			}
			ss.buf.mu.Lock()
			bl, ch := len(ss.buf.backlog), len(ss.buf.c)
			ss.buf.mu.Unlock()
			b, bcap, bsum := pool.outstanding(true)
			t.Logf("2. fully drained (%d bytes, in order=%v) and all delivered buffers freed: backlog=%d chan=%d ; recvBuffer-acquired pool buffers outstanding=%d (%d bytes) [%s] ; %s",
				n, bytes.Equal(got, want), bl, ch, b, bcap, bsum, probeFieldState(&ss.buf))

			if end == "RST_STREAM" {
				if err := c.fr.WriteRSTStream(1, http2.ErrCodeCancel); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := c.fr.WriteData(1, true, nil); err != nil {
					t.Fatal(err)
				}
			}
			// wait for termination: stream removed from the transport's active set
			deadline := time.Now().Add(10 * time.Second)
			for end == "RST_STREAM" {
				st.mu.Lock()
				_, active := st.activeStreams[1]
				st.mu.Unlock()
				if !active || time.Now().After(deadline) {
					t.Logf("3. after %s: stream still in activeStreams=%v ; stream ctx err=%v ; state=%v(streamDone=%v)", end, active, ss.Context().Err(), ss.getState(), ss.getState() == streamDone)
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
			if end != "RST_STREAM" { // consume the terminal EOF like an application would
				_, err := ss.readTo(make([]byte, 1))
				t.Logf("   read after END_STREAM -> %v", err)
			}
			time.Sleep(300 * time.Millisecond)
			runtime.GC()
			d, dcap, dsum := pool.outstanding(true)
			t.Logf("RESULT %s: recvBuffer-acquired pool buffers NOT returned to the pool after termination=%d (%d bytes) [%s] ; pool.Put calls total=%d ; %s",
				end, d, dcap, dsum, pool.puts, probeFieldState(&ss.buf))
		})
	}
}

// ---------------------------------------------------------------------------
// C5: fixture's IncrementalMessageAssembly workload (1,024 x three 1-byte
// frames, each burst drained, delivered payloads retained until the end) with
// a selectable pool.
// ---------------------------------------------------------------------------

func probeAssembly(t *testing.T, label string, inner mem.BufferPool) {
	pool := newProbePool(inner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: probeDummyH2Server(t)}, ConnectOptions{BufferPool: pool}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatal(err)
	}
	defer ct.Close(errors.New("probe done"))
	s := ct.(*http2Client).newStream(context.Background(), &CallHdr{}, nil)
	b := &s.buf
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	reader := &recvBufferReader{ctx: rctx, ctxDone: rctx.Done(), recv: b}
	var retained mem.BufferSlice
	defer func() { retained.Free() }()

	before := pool.numGets()
	const cycles = 1024
	var want, got []byte
	for i := 0; i < cycles; i++ {
		for j := 0; j < 3; j++ {
			p := []byte{byte((i*7+j)%251 + 1)}
			b.put(recvMsg{buffer: mem.Copy(p, pool)})
			want = append(want, p...)
		}
		for remaining := 3; remaining > 0; {
			buf, err := reader.Read(remaining)
			if err != nil {
				t.Fatalf("cycle %d: %v", i, err)
			}
			remaining -= buf.Len()
			got = append(got, buf.ReadOnlyData()...)
			retained = append(retained, buf)
		}
	}
	// distinct backing arrays retained by the delivered payloads
	type agg struct{ capacity int }
	seen := map[uintptr]*agg{}
	poolBacked := map[uintptr]bool{}
	for _, buf := range retained {
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
		if cap(d) > a.capacity {
			a.capacity = cap(d)
		}
		if _, ok := pool.owner(d); ok {
			poolBacked[end] = true
		}
	}
	distinctCap := 0
	for _, a := range seen {
		distinctCap += a.capacity
	}
	gets, sum := pool.getsSince(before)
	on, ocap, osum := pool.outstanding(true)
	t.Logf("%s: payload=%d bytes in order=%v ; delivered buffers retained=%d ; distinct backing arrays=%d (pool-backed=%d) ; distinct retained backing capacity=%d bytes (%.2f MiB)",
		label, len(want), bytes.Equal(got, want), len(retained), len(seen), len(poolBacked), distinctCap, float64(distinctCap)/(1<<20))
	t.Logf("%s: pool Get calls during workload=%d [%s]", label, gets, sum)
	t.Logf("RESULT %s: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=%d, %d bytes (%.2f MiB) [%s]", label, on, ocap, float64(ocap)/(1<<20), osum)
}

func TestVerifyProbe_C5_BurstAssembly(t *testing.T) {
	t.Run("pool=4KiB-minimum NewBinaryTieredBufferPool(12,14,15,20)", func(t *testing.T) {
		p, err := mem.NewBinaryTieredBufferPool(12, 14, 15, 20)
		if err != nil {
			t.Fatal(err)
		}
		probeAssembly(t, "4KiB-min pool", p)
	})
	t.Run("pool=default", func(t *testing.T) {
		probeAssembly(t, "default pool", mem.DefaultBufferPool())
	})
}

// C5 over the wire: the application assembles one 3072-byte message with a
// single Stream.read while a raw HTTP/2 peer trickles it in as 1,024 bursts of
// three 1-byte DATA frames. Timing-dependent by nature; reports what happened.
func TestVerifyProbe_C5_OverWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inner, err := mem.NewBinaryTieredBufferPool(12, 14, 15, 20)
	if err != nil {
		t.Fatal(err)
	}
	pool := newProbePool(inner)
	ps := startProbeServer(t, ctx, pool)
	c := dialProbeClient(t, ps.lis.Addr().String())
	c.headers(t, 1)
	ss := probeWaitStream(t, ps)
	const bursts = 1024
	type res struct {
		data mem.BufferSlice
		err  error
	}
	done := make(chan res, 1)
	go func() {
		d, err := ss.read(3 * bursts) // application waits for the whole message
		done <- res{d, err}
	}()
	var want []byte
	for i := 0; i < bursts; i++ {
		for j := 0; j < 3; j++ {
			p := []byte{byte((i*7+j)%251 + 1)}
			want = append(want, p...)
			if err := c.fr.WriteData(1, false, p); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(200 * time.Microsecond)
	}
	var r res
	select {
	case r = <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for read")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	on, ocap, osum := pool.outstanding(true)
	gets, _ := pool.getsSince(0)
	t.Logf("RESULT over-the-wire: message of %d bytes assembled (in order=%v) from %d buffers ; pool Get calls=%d ; recvBuffer-acquired pool buffers retained by the unfinished message=%d, %d bytes (%.2f MiB) [%s]",
		len(want), bytes.Equal(r.data.Materialize(), want), len(r.data), gets, on, ocap, float64(ocap)/(1<<20), osum)
	r.data.Free()
	on2, _, _ := pool.outstanding(true)
	t.Logf("after freeing the message: recvBuffer-acquired pool buffers outstanding=%d", on2)
}
