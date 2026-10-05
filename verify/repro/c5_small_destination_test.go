// Run: git worktree add --detach /tmp/wt-a4df28bb <claims-remote>/evalon/grpc-go-tr-a4df28bb && cp verify/repro/c5_small_destination_test.go /tmp/wt-a4df28bb/internal/transport/verify_c5_small_destination_test.go && cp <eval_tests.zip>/tests/eval_recv_buffer_compaction_test.go /tmp/wt-a4df28bb/internal/transport/ && cd /tmp/wt-a4df28bb && go test -v -run '^TestVerify_C5_' ./internal/transport -race -count=1
// (uses evalNewProductionHTTP2Client from the eval fixture file to get a real client stream)

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/mem"
)

// verifyC5Pool records capacity and acquiring call stack for every Get, and
// every Put. With inner == nil it returns exact-capacity storage.
type verifyC5Pool struct {
	inner mem.BufferPool
	mu    sync.Mutex
	out   map[uintptr]string
	log   []string
}

func verifyC5Stack() string {
	pcs := make([]uintptr, 6)
	n := runtime.Callers(3, pcs)
	fr := runtime.CallersFrames(pcs[:n])
	var names []string
	for {
		f, more := fr.Next()
		names = append(names, f.Function[strings.LastIndex(f.Function, ".")+1:])
		if !more || len(names) == 2 {
			break
		}
	}
	return strings.Join(names, "<-")
}

func (p *verifyC5Pool) Get(n int) *[]byte {
	var b *[]byte
	if p.inner != nil {
		b = p.inner.Get(n)
	} else {
		s := make([]byte, n)
		b = &s
	}
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	desc := fmt.Sprintf("Get(%d)->cap %d by %s", n, cap(*b), verifyC5Stack())
	p.out[ptr] = desc
	p.log = append(p.log, desc)
	p.mu.Unlock()
	return b
}

func (p *verifyC5Pool) Put(b *[]byte) {
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	p.log = append(p.log, fmt.Sprintf("Put(cap %d) by %s", cap(*b), verifyC5Stack()))
	delete(p.out, ptr)
	p.mu.Unlock()
	if p.inner != nil {
		p.inner.Put(b)
	}
}

func verifyC5Run(t *testing.T, pool *verifyC5Pool, frames int) (outstanding int) {
	client := evalNewProductionHTTP2Client(t, pool)
	s := client.newStream(context.Background(), &CallHdr{}, nil)

	var want []byte
	for i := range frames {
		p := make([]byte, 100)
		for j := range p {
			p[j] = byte((i*11+j)%251 + 1)
		}
		want = append(want, p...)
		s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
		if i == 0 {
			if len(s.buf.c) != 1 {
				t.Fatalf("first frame did not enter the channel")
			}
			t.Logf("first frame entered the channel (len(c)=%d, backlog=%d)", len(s.buf.c), len(s.buf.backlog))
		}
	}

	// Flush, consume and free.
	var got []byte
	var delivered []string
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < len(want) {
		if time.Now().After(deadline) {
			t.Fatalf("drain timeout")
		}
		s.buf.load()
		select {
		case m := <-s.buf.c:
			d := m.buffer.ReadOnlyData()
			got = append(got, d...)
			delivered = append(delivered, fmt.Sprintf("%T len=%d cap=%d", m.buffer, len(d), cap(d)))
			m.buffer.Free()
		default:
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	t.Logf("delivered buffers (each Free()d): %v", delivered)

	// Terminate the stream through the transport: closeStream delivers io.EOF
	// to the receive buffer, which the reader then consumes.
	client.closeStream(s, io.EOF, false, 0, nil, nil, true)
	s.buf.load()
	if m := <-s.buf.c; m.err != io.EOF {
		t.Fatalf("want EOF, got %+v", m)
	}
	t.Logf("stream terminated: state==streamDone:%v recvBuffer.err=%v pending==nil:%v backlog=%d", s.getState() == streamDone, s.buf.err, s.buf.pending == nil, len(s.buf.backlog))
	client.Close(fmt.Errorf("done"))
	time.Sleep(100 * time.Millisecond)

	pool.mu.Lock()
	defer pool.mu.Unlock()
	for _, l := range pool.log {
		t.Logf("  pool trace: %s", l)
	}
	for ptr, d := range pool.out {
		t.Logf("  OUTSTANDING after free + stream termination + transport close: %#x %s", ptr, d)
	}
	return len(pool.out)
}

func verifyC5Tiered(t *testing.T) mem.BufferPool {
	p, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20) // same tiers as mem.DefaultBufferPool
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerify_C5_NineFrames_ExactCapacityPool(t *testing.T) {
	n := verifyC5Run(t, &verifyC5Pool{out: map[uintptr]string{}}, 9)
	t.Logf("RESULT C5 nine 100-byte frames, exact-capacity pool: outstanding=%d", n)
}

func TestVerify_C5_TenFrames_ExactCapacityPool(t *testing.T) {
	n := verifyC5Run(t, &verifyC5Pool{out: map[uintptr]string{}}, 10)
	t.Logf("RESULT C5 ten 100-byte frames (the fixture's count), exact-capacity pool: outstanding=%d", n)
}

func TestVerify_C5_NineFrames_DefaultTiers(t *testing.T) {
	n := verifyC5Run(t, &verifyC5Pool{inner: verifyC5Tiered(t), out: map[uintptr]string{}}, 9)
	t.Logf("RESULT C5 nine 100-byte frames, default-tier pool: outstanding=%d", n)
}

func TestVerify_C5_ThreeFrames_DefaultTiers(t *testing.T) {
	n := verifyC5Run(t, &verifyC5Pool{inner: verifyC5Tiered(t), out: map[uintptr]string{}}, 3)
	t.Logf("RESULT C5 three 100-byte frames, default-tier pool: outstanding=%d", n)
}
