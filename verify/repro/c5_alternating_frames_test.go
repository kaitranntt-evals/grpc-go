// Run (from the root of a grpc-go checkout of this verify branch): git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-ae5dffd3 && git worktree add --detach /tmp/wt-ae5dffd3 FETCH_HEAD && cp verify/repro/c5_alternating_frames_test.go /tmp/wt-ae5dffd3/internal/transport/zz_verify_c5_test.go && (cd /tmp/wt-ae5dffd3 && go test ./internal/transport -run 'TestVerifyC5' -count=1 -v)

package transport

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
)

// c5ExactPool hands out buffers of exactly the requested capacity and tracks
// which ones are live (Get without Put).
type c5ExactPool struct {
	mu   sync.Mutex
	live map[*[]byte]int
	gets []int
}

func (p *c5ExactPool) Get(n int) *[]byte {
	b := make([]byte, n)
	p.mu.Lock()
	p.live[&b] = n
	p.gets = append(p.gets, n)
	p.mu.Unlock()
	return &b
}

func (p *c5ExactPool) Put(b *[]byte) {
	p.mu.Lock()
	delete(p.live, b)
	p.mu.Unlock()
}

func (p *c5ExactPool) liveBytes() (n, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.live {
		total += c
	}
	return len(p.live), total
}

// c5Run enqueues thirty alternating 1-byte / 2-KiB frames with reads delayed
// and accounts for every queued buffer's payload and backing capacity.
func c5Run(t *testing.T, compaction bool) (payload, capacity int) {
	old := envconfig.EnableReceiveBufferCompaction
	envconfig.EnableReceiveBufferCompaction = compaction
	defer func() { envconfig.EnableReceiveBufferCompaction = old }()

	pool := &c5ExactPool{live: map[*[]byte]int{}}
	b := &recvBuffer{}
	b.init(pool)

	// Source identity: pointer to first payload byte of every frame we put.
	srcPtr := map[*byte]int{}
	var want []byte
	for i := 0; i < 30; i++ {
		size := 1
		if i%2 == 1 {
			size = 2048
		}
		p := bytes.Repeat([]byte{byte(i + 1)}, size)
		want = append(want, p...)
		buf := mem.Copy(p, pool) // 2-KiB frames come from the pool, 1-byte frames are SliceBuffers (as the framer produces them)
		srcPtr[&buf.ReadOnlyData()[0]] = i
		b.put(recvMsg{buffer: buf})
	}

	b.mu.Lock()
	t.Logf("compaction=%v: channel holds frame 0 (1 byte); backlog has %d entries, open chunk=%v", compaction, len(b.backlog), b.chunk != nil)
	var destCap, destPayload, origCap, origPayload, nDest int
	for i, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		kind := "DESTINATION (compaction-created)"
		if src, ok := srcPtr[&d[0]]; ok {
			kind = fmt.Sprintf("original frame %d", src)
			origCap += cap(d)
			origPayload += len(d)
		} else {
			nDest++
			destCap += cap(d)
			destPayload += len(d)
		}
		t.Logf("  backlog[%2d] payload=%-5d backing_cap=%-6d %s", i, len(d), cap(d), kind)
	}
	if b.chunk != nil {
		nDest++
		destCap += cap(*b.chunk)
		destPayload += len(*b.chunk)
		t.Logf("  open chunk  payload=%-5d backing_cap=%-6d DESTINATION (compaction-created)", len(*b.chunk), cap(*b.chunk))
	}
	b.mu.Unlock()
	ln, lb := pool.liveBytes()
	payload, capacity = origPayload+destPayload, origCap+destCap
	t.Logf("compaction=%v SUMMARY: queued payload=%d bytes; live backing capacity=%d bytes (%.2fx payload)", compaction, payload, capacity, float64(capacity)/float64(payload))
	t.Logf("compaction=%v SUMMARY: compaction destinations=%d holding %d payload bytes in %d bytes of capacity; original buffers hold %d payload bytes in %d bytes of capacity", compaction, nDest, destPayload, destCap, origPayload, origCap)
	t.Logf("compaction=%v SUMMARY: exact-capacity pool: Get sizes=%v; live pool buffers=%d (%d bytes)", compaction, pool.gets, ln, lb)

	// Drain and verify ordering.
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < len(want) && time.Now().Before(deadline) {
		select {
		case m := <-b.get():
			got = append(got, m.buffer.ReadOnlyData()...)
			m.buffer.Free()
			b.load()
		default:
			b.load()
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch: got %d bytes want %d", len(got), len(want))
	}
	return payload, capacity
}

func TestVerifyC5_AlternatingTinyAnd2KiBFrames(t *testing.T) {
	offPayload, offCap := c5Run(t, false)
	onPayload, onCap := c5Run(t, true)
	t.Logf("C5 RESULT: compaction disabled: capacity/payload = %d/%d = %.2fx; compaction enabled: %d/%d = %.2fx; enabled holds %d more bytes of backing capacity than disabled for the same queued payload",
		offCap, offPayload, float64(offCap)/float64(offPayload), onCap, onPayload, float64(onCap)/float64(onPayload), onCap-offCap)
	if onCap > 2*onPayload {
		t.Errorf("PROBLEM REPRODUCED: with compaction enabled the stream retains %d bytes of backing capacity for %d bytes of queued payload", onCap, onPayload)
	}
}

// End-to-end variant: a real http2Server with the default buffer pool receives
// the same thirty alternating 1-byte / 2-KiB DATA frames on a stream that the
// application has not started reading.
func c5E2E(t *testing.T, compaction bool) (payload, capacity int) {
	old := envconfig.EnableReceiveBufferCompaction
	envconfig.EnableReceiveBufferCompaction = compaction
	defer func() { envconfig.EnableReceiveBufferCompaction = old }()

	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)
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

	var want []byte
	for i := 0; i < 30; i++ {
		size := 1
		if i%2 == 1 {
			size = 2048
		}
		p := bytes.Repeat([]byte{byte(i + 1)}, size)
		want = append(want, p...)
		mu.Lock()
		err := fr.WriteData(1, false, p)
		mu.Unlock()
		if err != nil {
			t.Fatalf("WriteData: %v", err)
		}
	}
	waitWhileTrue(t, func() (bool, error) {
		ss.fc.mu.Lock()
		got := int(ss.fc.pendingData)
		ss.fc.mu.Unlock()
		if got != len(want) {
			return true, fmt.Errorf("pendingData=%d want %d", got, len(want))
		}
		time.Sleep(20 * time.Millisecond)
		return false, nil
	})

	b := &ss.buf
	b.mu.Lock()
	tinyEntries, tinyCap := 0, 0
	for _, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		payload += len(d)
		capacity += cap(d)
		if len(d) == 1 {
			tinyEntries++
			tinyCap += cap(d)
		}
	}
	n := len(b.backlog)
	b.mu.Unlock()
	t.Logf("e2e compaction=%v: %d backlog entries; queued payload=%d bytes; live backing capacity=%d bytes (%.2fx); the %d one-byte entries hold %d bytes of capacity", compaction, n, payload, capacity, float64(capacity)/float64(payload), tinyEntries, tinyCap)

	got := make([]byte, len(want))
	if _, err := ss.readTo(got); err != nil {
		t.Fatalf("readTo: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	return payload, capacity
}

func TestVerifyC5_EndToEndServerTransport(t *testing.T) {
	_, offCap := c5E2E(t, false)
	onPayload, onCap := c5E2E(t, true)
	t.Logf("C5 E2E RESULT: same 30 DATA frames, same %d queued payload bytes: backing capacity %d bytes with compaction disabled vs %d bytes with compaction enabled (+%d)", onPayload, offCap, onCap, onCap-offCap)
	if onCap > offCap {
		t.Errorf("PROBLEM REPRODUCED end-to-end: enabling compaction increases retained backing capacity from %d to %d bytes", offCap, onCap)
	}
}
