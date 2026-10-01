// Run (from the root of a grpc-go checkout of this verify branch): git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-e7cea533 && git worktree add --detach /tmp/wt-e7cea533 FETCH_HEAD && cp verify/repro/c1_c7_pool_leak_test.go /tmp/wt-e7cea533/internal/transport/zz_verify_c1_c7_test.go && (cd /tmp/wt-e7cea533 && go test ./internal/transport -run 'TestVerifyC1|TestVerifyC7' -count=1 -v)

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"google.golang.org/grpc/mem"
)

// verifyTrackingPool hands out buffers of exactly the requested capacity and
// records every Get/Put, classifying Gets made from the recvBuffer compaction
// path (any frame whose function name contains "recvBuffer") as compaction
// destinations.
type verifyTrackingPool struct {
	mu   sync.Mutex
	gets map[*[]byte]verifyAcq
	puts map[*[]byte]int
	// putsUnknown counts Puts of pointers this pool never handed out.
	putsUnknown int
}

type verifyAcq struct {
	cap         int
	destination bool
	site        string
}

func newVerifyTrackingPool() *verifyTrackingPool {
	return &verifyTrackingPool{gets: map[*[]byte]verifyAcq{}, puts: map[*[]byte]int{}}
}

func (p *verifyTrackingPool) Get(n int) *[]byte {
	b := make([]byte, n)
	pcs := make([]uintptr, 16)
	pcs = pcs[:runtime.Callers(2, pcs)]
	frames := runtime.CallersFrames(pcs)
	dest := false
	site := ""
	for {
		f, more := frames.Next()
		if site == "" {
			site = f.Function[strings.LastIndex(f.Function, "/")+1:]
		}
		if strings.Contains(f.Function, "recvBuffer") {
			dest = true
			site = f.Function[strings.LastIndex(f.Function, "/")+1:]
			break
		}
		if !more {
			break
		}
	}
	p.mu.Lock()
	p.gets[&b] = verifyAcq{cap: cap(b), destination: dest, site: site}
	p.mu.Unlock()
	return &b
}

func (p *verifyTrackingPool) Put(b *[]byte) {
	p.mu.Lock()
	if _, ok := p.gets[b]; ok {
		p.puts[b]++
	} else {
		p.putsUnknown++
	}
	p.mu.Unlock()
}

// report returns per-(kind,capacity) acquired/returned/outstanding counts.
func (p *verifyTrackingPool) report() (lines []string, destOutstandingSmall, destOutstandingAll, destAcquired int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	type key struct {
		dest bool
		cap  int
		site string
	}
	acq := map[key]int{}
	ret := map[key]int{}
	for ptr, a := range p.gets {
		k := key{a.destination, a.cap, a.site}
		acq[k]++
		if p.puts[ptr] > 0 {
			ret[k]++
		}
	}
	keys := make([]key, 0, len(acq))
	for k := range acq {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dest != keys[j].dest {
			return keys[i].dest
		}
		if keys[i].cap != keys[j].cap {
			return keys[i].cap < keys[j].cap
		}
		return keys[i].site < keys[j].site
	})
	for _, k := range keys {
		kind := "other      "
		if k.dest {
			kind = "destination"
			destAcquired += acq[k]
			destOutstandingAll += acq[k] - ret[k]
			if k.cap <= 1024 {
				destOutstandingSmall += acq[k] - ret[k]
			}
		}
		lines = append(lines, fmt.Sprintf("%s cap=%-6d acquired=%-4d returned=%-4d outstanding=%-4d via %s", kind, k.cap, acq[k], ret[k], acq[k]-ret[k], k.site))
	}
	return
}

// drainAll consumes every queued payload byte, freeing each delivered buffer.
func verifyDrainAll(t *testing.T, b *recvBuffer, want int) []byte {
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

// C1: one burst of 2,000 one-byte frames against an undrained recvBuffer, then
// full consumption + Free of everything delivered.
func TestVerifyC1_SmallCompactionDestinationsNeverReturned(t *testing.T) {
	pool := newVerifyTrackingPool()
	b := &recvBuffer{}
	b.init(pool)

	const n = 2000
	var want []byte
	for i := 0; i < n; i++ {
		v := byte(i%251 + 1)
		want = append(want, v)
		b.put(recvMsg{buffer: mem.SliceBuffer{v}})
	}
	got := verifyDrainAll(t, b, n)
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	// Release receive-buffer ownership: terminal error + final load.
	b.put(recvMsg{err: io.EOF})
	b.load()
	select {
	case <-b.get():
	default:
	}
	b.mu.Lock()
	t.Logf("after drain: backlog=%d chunk-nil=%v", len(b.backlog), b.chunk == nil)
	b.mu.Unlock()

	lines, small, all, acquired := pool.report()
	for _, l := range lines {
		t.Log(l)
	}
	t.Logf("C1 RESULT: compaction destinations acquired=%d, outstanding(all caps)=%d, outstanding(cap<=1024)=%d", acquired, all, small)
	if small > 0 {
		t.Errorf("PROBLEM REPRODUCED: %d compaction destination(s) with cap<=1024 were acquired from the configured pool and never returned", small)
	}
}

// C7: repeated short bursts (3 one-byte frames each), each fully consumed and
// freed before the next burst starts.
func TestVerifyC7_ShortBurstDestinationsNeverReturned(t *testing.T) {
	pool := newVerifyTrackingPool()
	b := &recvBuffer{}
	b.init(pool)

	const bursts = 50
	for i := 0; i < bursts; i++ {
		for j := 0; j < 3; j++ {
			b.put(recvMsg{buffer: mem.SliceBuffer{byte(j + 1)}})
		}
		if got := verifyDrainAll(t, b, 3); !bytes.Equal(got, []byte{1, 2, 3}) {
			t.Fatalf("burst %d: payload mismatch %v", i, got)
		}
	}
	b.put(recvMsg{err: io.EOF})
	b.load()
	select {
	case <-b.get():
	default:
	}
	b.mu.Lock()
	t.Logf("after %d bursts: backlog=%d chunk-nil=%v", bursts, len(b.backlog), b.chunk == nil)
	b.mu.Unlock()

	lines, small, all, acquired := pool.report()
	for _, l := range lines {
		t.Log(l)
	}
	t.Logf("C7 RESULT: bursts=%d compaction destinations acquired=%d outstanding(all caps)=%d outstanding(cap<=1024)=%d", bursts, acquired, all, small)
	if all > 0 {
		t.Errorf("PROBLEM REPRODUCED: %d of %d compaction destination acquisitions have no matching pool return", all, acquired)
	}
}

// End-to-end variant: a real http2Server configured with the tracking pool
// receives short bursts of one-byte DATA frames on one stream; the application
// reads each burst completely, then the stream and connection are torn down.
func TestVerifyC1C7_EndToEndServerTransport(t *testing.T) {
	pool := newVerifyTrackingPool()
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: pool}, suspended)
	defer server.stop()

	mconn, err := net.Dial("tcp", server.lis.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer mconn.Close()
	if _, err := mconn.Write(clientPreface); err != nil {
		t.Fatalf("preface: %v", err)
	}
	var mu sync.Mutex
	framer := http2.NewFramer(mconn, mconn)
	if err := framer.WriteSettings(); err != nil {
		t.Fatalf("settings: %v", err)
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			frame, err := framer.ReadFrame()
			if err != nil {
				return
			}
			if pf, ok := frame.(*http2.PingFrame); ok && !pf.IsAck() {
				mu.Lock()
				framer.WritePing(true, pf.Data)
				mu.Unlock()
			}
		}
	}()
	var hbuf bytes.Buffer
	henc := hpack.NewEncoder(&hbuf)
	for _, hf := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"},
		{Name: ":path", Value: "foo"},
		{Name: ":authority", Value: "localhost"},
		{Name: "content-type", Value: "application/grpc"},
	} {
		henc.WriteField(hf)
	}
	mu.Lock()
	err = framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true})
	mu.Unlock()
	if err != nil {
		t.Fatalf("headers: %v", err)
	}

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

	const bursts, perBurst = 20, 8
	total := 0
	for i := 0; i < bursts; i++ {
		for j := 0; j < perBurst; j++ {
			mu.Lock()
			err := framer.WriteData(1, false, []byte{byte(j + 1)})
			mu.Unlock()
			if err != nil {
				t.Fatalf("data: %v", err)
			}
		}
		total += perBurst
		// Wait until the transport has received the whole burst, unread.
		waitWhileTrue(t, func() (bool, error) {
			sstream.fc.mu.Lock()
			defer sstream.fc.mu.Unlock()
			if int(sstream.fc.pendingData) != perBurst {
				return true, fmt.Errorf("pendingData=%d want %d", sstream.fc.pendingData, perBurst)
			}
			return false, nil
		})
		got := make([]byte, perBurst)
		if _, err := sstream.readTo(got); err != nil {
			t.Fatalf("readTo: %v", err)
		}
		for j := range got {
			if got[j] != byte(j+1) {
				t.Fatalf("burst %d byte %d = %d", i, j, got[j])
			}
		}
	}
	mu.Lock()
	framer.WriteData(1, true, nil)
	mu.Unlock()
	if _, err := sstream.readTo(make([]byte, 1)); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	mconn.Close()
	<-readerDone
	server.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	<-ctx.Done() // let transport goroutines finish returning buffers

	lines, small, all, acquired := pool.report()
	for _, l := range lines {
		t.Log(l)
	}
	t.Logf("E2E RESULT: bursts=%d x %d one-byte DATA frames; compaction destinations acquired=%d outstanding(all caps)=%d outstanding(cap<=1024)=%d", bursts, perBurst, acquired, all, small)
	if all > 0 {
		t.Errorf("PROBLEM REPRODUCED end-to-end: %d of %d compaction destination acquisitions were never returned to the transport's configured pool", all, acquired)
	}
}
