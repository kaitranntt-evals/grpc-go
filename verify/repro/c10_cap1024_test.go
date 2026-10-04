//go:build verify_audit

// C10 repro: copy to internal/transport/ on evalon/grpc-go-tr-d4a3af3a, then: go test -tags verify_audit -v -run '^TestC10_' google.golang.org/grpc/internal/transport -race -count=1

package transport

// Probe/repro for C10 on evalon/grpc-go-tr-d4a3af3a: does a capacity-1024
// compaction destination acquired from the configured pool get returned once
// the resulting message buffer is consumed and freed?
// Run: go test -tags verify_audit -v -run '^TestC10_' google.golang.org/grpc/internal/transport -race -count=1

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/mem"
)

type c10Track struct {
	inner mem.BufferPool
	mu    sync.Mutex
	out   map[uintptr]int
	gets  []string
	puts  []int
}

func (p *c10Track) Get(n int) *[]byte {
	b := p.inner.Get(n)
	p.mu.Lock()
	p.out[uintptr(unsafe.Pointer(unsafe.SliceData(*b)))] = cap(*b)
	p.gets = append(p.gets, fmt.Sprintf("Get(%d)->len=%d,cap=%d", n, len(*b), cap(*b)))
	p.mu.Unlock()
	return b
}

func (p *c10Track) Put(b *[]byte) {
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	if c, ok := p.out[ptr]; ok {
		delete(p.out, ptr)
		p.puts = append(p.puts, c)
	}
	p.mu.Unlock()
	p.inner.Put(b)
}

// c10ExactPool is a contract-compliant pool: Get(n) returns len n, cap n.
type c10ExactPool struct{}

func (c10ExactPool) Get(n int) *[]byte { b := make([]byte, n); return &b }
func (c10ExactPool) Put(*[]byte)       {}

func c10Run(t *testing.T, name string, inner mem.BufferPool, frames, frameSize int) (outstanding int) {
	tp := &c10Track{inner: inner, out: map[uintptr]int{}}
	s := (&http2Client{bufferPool: tp}).newStream(context.Background(), &CallHdr{}, nil)
	var want, got []byte
	for i := 0; i < frames; i++ {
		p := bytes.Repeat([]byte{byte(i%251 + 1)}, frameSize)
		want = append(want, p...)
		s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
	}
	deadline := time.Now().Add(5 * time.Second)
	var delivered []string
	for len(got) < len(want) && time.Now().Before(deadline) {
		s.buf.load()
		select {
		case m := <-s.buf.c:
			d := m.buffer.ReadOnlyData()
			delivered = append(delivered, fmt.Sprintf("%T(len=%d,cap=%d)", m.buffer, len(d), cap(d)))
			got = append(got, d...)
			m.buffer.Free() // payload consumed and released
		default:
		}
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	var leaked []int
	for _, c := range tp.out {
		leaked = append(leaked, c)
	}
	t.Logf("C10 pool=%s frames=%dx%dB inOrder=%v\n      acquisitions=%v\n      delivered=%v\n      returned caps=%v\n      NOT returned after Free() (caps)=%v",
		name, frames, frameSize, bytes.Equal(got, want), tp.gets, delivered, tp.puts, leaked)
	return len(leaked)
}

func TestC10_Cap1024Destination(t *testing.T) {
	// Pools whose Get(1024) yields capacity exactly 1024.
	if n := c10Run(t, "mem.NewTieredBufferPool(1024,4096,16384)", mem.NewTieredBufferPool(1024, 4096, 16384), 10, 100); n > 0 {
		t.Errorf("tiered pool with a 1KiB tier: %d acquired destination(s) never returned", n)
	}
	if n := c10Run(t, "exact-length pool (make([]byte,n))", c10ExactPool{}, 10, 100); n > 0 {
		t.Errorf("exact pool: %d acquired destination(s) never returned", n)
	}
	// Enough data to roll over into the 2048-capacity destination as well.
	if n := c10Run(t, "mem.NewTieredBufferPool(1024,4096,16384) [3000 bytes]", mem.NewTieredBufferPool(1024, 4096, 16384), 30, 100); n > 0 {
		t.Errorf("tiered pool, 3000 bytes: %d acquired destination(s) never returned", n)
	}
}

// Control: the default pool has no 1KiB tier, so Get(1024) yields cap 4096.
func TestC10_DefaultPoolControl(t *testing.T) {
	if n := c10Run(t, "mem.DefaultBufferPool()", mem.DefaultBufferPool(), 10, 100); n > 0 {
		t.Errorf("default pool: %d acquired destination(s) never returned", n)
	}
}
