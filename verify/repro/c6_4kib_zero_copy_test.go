// Run (from the root of a grpc-go checkout of this verify branch): git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-8cebbc22 && git worktree add --detach /tmp/wt-8cebbc22 FETCH_HEAD && cp verify/repro/c6_4kib_zero_copy_test.go /tmp/wt-8cebbc22/internal/transport/zz_verify_c6_test.go && (cd /tmp/wt-8cebbc22 && go test ./internal/transport -run 'TestVerifyC6' -count=1 -v)

package transport

import (
	"bytes"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
)

// c6Pool hands out exact-capacity buffers, numbers them, and logs every Get
// and Put with the calling recvBuffer method (if any).
type c6Pool struct {
	mu     sync.Mutex
	id     map[*[]byte]int
	first  map[*byte]int // first backing byte -> buffer id
	events []string
	next   int
	// counters
	recvGets, recvPuts int
	dest               map[int]bool // ids acquired by recvBuffer methods
	srcPuts            int          // Puts (by anyone) of 4-KiB source buffers
}

func newC6Pool() *c6Pool {
	return &c6Pool{id: map[*[]byte]int{}, first: map[*byte]int{}, dest: map[int]bool{}}
}

func c6Caller() string {
	pcs := make([]uintptr, 16)
	pcs = pcs[:runtime.Callers(3, pcs)]
	frames := runtime.CallersFrames(pcs)
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "recvBuffer") {
			return f.Function[strings.LastIndex(f.Function, ".")+1:]
		}
		if !more {
			return ""
		}
	}
}

func (p *c6Pool) Get(n int) *[]byte {
	b := make([]byte, n)
	who := c6Caller()
	p.mu.Lock()
	p.next++
	p.id[&b] = p.next
	p.first[&b[0]] = p.next
	if who != "" {
		p.recvGets++
		p.dest[p.next] = true
		p.events = append(p.events, fmt.Sprintf("Get(%d) -> buf#%d  by recvBuffer.%s  [compaction DESTINATION]", n, p.next, who))
	} else {
		p.events = append(p.events, fmt.Sprintf("Get(%d) -> buf#%d  [source DATA buffer]", n, p.next))
	}
	p.mu.Unlock()
	return &b
}

func (p *c6Pool) Put(b *[]byte) {
	who := c6Caller()
	p.mu.Lock()
	if !p.dest[p.id[b]] && cap(*b) == 4096 {
		p.srcPuts++
	}
	if who != "" {
		p.recvPuts++
		p.events = append(p.events, fmt.Sprintf("Put(buf#%d)       by recvBuffer.%s  [source released inside put(), before any read]", p.id[b], who))
	} else {
		p.events = append(p.events, fmt.Sprintf("Put(buf#%d)       by reader", p.id[b]))
	}
	p.mu.Unlock()
}

func (p *c6Pool) bufID(d []byte) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.first[&d[0]]
}

func c6Run(t *testing.T, compaction bool) (sourcesDeliveredIntact, copies int) {
	old := envconfig.EnableReceiveBufferCompaction
	envconfig.EnableReceiveBufferCompaction = compaction
	defer func() { envconfig.EnableReceiveBufferCompaction = old }()

	pool := newC6Pool()
	b := &recvBuffer{}
	b.init(pool)

	const frames, size = 8, 4096
	var want []byte
	srcID := map[int]bool{}
	for i := 0; i < frames; i++ {
		p := bytes.Repeat([]byte{byte(i + 1)}, size)
		want = append(want, p...)
		buf := mem.Copy(p, pool) // pooled 4-KiB buffer, exactly what the framer hands the transport
		srcID[pool.bufID(buf.ReadOnlyData())] = true
		b.put(recvMsg{buffer: buf})
	}
	pool.mu.Lock()
	pool.events = append(pool.events, "---- all 8 frames enqueued; reader has consumed nothing yet ----")
	pool.mu.Unlock()

	b.mu.Lock()
	t.Logf("compaction=%v: after enqueueing %d pooled %d-byte buffers with no reads: channel=1 msg, backlog=%d entries", compaction, frames, size, len(b.backlog))
	for i, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		id := pool.bufID(d)
		kind := "ORIGINAL source buffer"
		if !srcID[id] {
			kind = "NEW compaction chunk (payload was copied)"
		}
		t.Logf("  backlog[%d] len=%-5d cap=%-5d buf#%-2d %s", i, len(d), cap(d), id, kind)
	}
	b.mu.Unlock()

	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < len(want) && time.Now().Before(deadline) {
		select {
		case m := <-b.get():
			d := m.buffer.ReadOnlyData()
			if srcID[pool.bufID(d)] && len(d) == size {
				sourcesDeliveredIntact++
			}
			got = append(got, d...)
			m.buffer.Free()
			b.load()
		default:
			b.load()
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	for _, e := range pool.events {
		t.Logf("  pool: %s", e)
	}
	t.Logf("compaction=%v RESULT: %d of %d original 4-KiB buffers reached the reader; recvBuffer acquired %d destination buffers and released %d source buffers itself", compaction, sourcesDeliveredIntact, frames, pool.recvGets, pool.recvPuts)
	return sourcesDeliveredIntact, pool.recvPuts
}

func TestVerifyC6_Homogeneous4KiBBuffers(t *testing.T) {
	offIntact, offCopies := c6Run(t, false)
	onIntact, onCopies := c6Run(t, true)
	t.Logf("C6 RESULT: originals delivered to reader: disabled=%d/8 enabled=%d/8; sources copied+released by recvBuffer before any read: disabled=%d enabled=%d", offIntact, onIntact, offCopies, onCopies)
	if onCopies > 0 {
		t.Errorf("PROBLEM REPRODUCED: with compaction enabled, %d of 8 homogeneous pooled 4-KiB buffers were copied into new chunks and released before the reader consumed anything", onCopies)
	}
}

// End-to-end: real http2Server, raw client sending 4,096-byte DATA frames to a
// stream that is not being read yet.
func c6E2E(t *testing.T, compaction bool) (intact, copied int) {
	old := envconfig.EnableReceiveBufferCompaction
	envconfig.EnableReceiveBufferCompaction = compaction
	defer func() { envconfig.EnableReceiveBufferCompaction = old }()

	pool := newC6Pool()
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: pool}, suspended)
	defer server.stop()
	conn, err := net.Dial("tcp", server.lis.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.Write(clientPreface)
	var mu sync.Mutex
	fr := http2.NewFramer(conn, conn)
	fr.WriteSettings()
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			if pf, ok := f.(*http2.PingFrame); ok && !pf.IsAck() {
				mu.Lock()
				fr.WritePing(true, pf.Data)
				mu.Unlock()
			}
		}
	}()
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, hf := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"}, {Name: ":path", Value: "foo"},
		{Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"},
	} {
		enc.WriteField(hf)
	}
	mu.Lock()
	fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true})
	mu.Unlock()
	var ss *ServerStream
	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		for k := range server.conns {
			st := k.(*http2Server)
			st.mu.Lock()
			for _, v := range st.activeStreams {
				if v.id == 1 {
					ss = v
				}
			}
			st.mu.Unlock()
		}
		if ss == nil {
			return true, fmt.Errorf("stream not created")
		}
		return false, nil
	})

	const frames, size = 12, 4096 // 49,152 bytes: inside the 64 KiB stream window
	total := 0
	for i := 0; i < frames; i++ {
		mu.Lock()
		err := fr.WriteData(1, false, bytes.Repeat([]byte{byte(i + 1)}, size))
		mu.Unlock()
		if err != nil {
			t.Fatalf("WriteData: %v", err)
		}
		total += size
	}
	waitWhileTrue(t, func() (bool, error) {
		ss.fc.mu.Lock()
		got := int(ss.fc.pendingData)
		ss.fc.mu.Unlock()
		if got != total {
			return true, fmt.Errorf("pendingData=%d want %d", got, total)
		}
		time.Sleep(20 * time.Millisecond)
		return false, nil
	})
	pool.mu.Lock()
	gets, srcPuts := pool.recvGets, pool.srcPuts
	pool.mu.Unlock()
	b := &ss.buf
	b.mu.Lock()
	var shape []string
	originals := 1 // the first frame sits in the channel untouched
	for _, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		kind := "chunk"
		if !pool.dest[pool.bufID(d)] {
			kind = "orig"
			originals++
		}
		shape = append(shape, fmt.Sprintf("%s:%d/%d", kind, len(d), cap(d)))
	}
	b.mu.Unlock()
	copied = frames - originals
	t.Logf("e2e compaction=%v: %d DATA frames of %d bytes received, none read yet: backlog (kind:len/cap)=%v; recvBuffer destination Gets=%d; original frame buffers still queued=%d; frames copied into chunks=%d; 4-KiB source buffers already returned to the pool before any read=%d", compaction, frames, size, shape, gets, originals, copied, srcPuts)
	got := make([]byte, total)
	if _, err := ss.readTo(got); err != nil {
		t.Fatalf("readTo: %v", err)
	}
	for i := 0; i < frames; i++ {
		if !bytes.Equal(got[i*size:(i+1)*size], bytes.Repeat([]byte{byte(i + 1)}, size)) {
			t.Fatalf("frame %d payload mismatch", i)
		}
	}
	return originals, copied
}

func TestVerifyC6_EndToEndServerTransport(t *testing.T) {
	_, offCopied := c6E2E(t, false)
	_, onCopied := c6E2E(t, true)
	t.Logf("C6 E2E RESULT: 4-KiB DATA frames copied+released before the application read: compaction disabled=%d, enabled=%d (of 12)", offCopied, onCopied)
	if onCopied > 0 {
		t.Errorf("PROBLEM REPRODUCED end-to-end: %d of 12 ordinary 4-KiB DATA frames lost their zero-copy path", onCopied)
	}
}
