// How to run (branch evalon/grpc-go-tr-b490959e): git apply verify/repro/c11_b490959e_mem_instrumentation.patch (counters only, in mem/buffers.go); cp this file to internal/transport/verify_c11_abandoned_wrapper_test.go && go test -v -run '^TestVerifyC11_' google.golang.org/grpc/internal/transport -race -count=1
//
// C11: when a writer refills the delivery channel before the reader calls
// recvBuffer.load and pooled pending data remains, does load() build a
// mem.NewBuffer(b.pending, b.pool) wrapper, fail the send, and abandon the
// wrapper without Free?
package transport

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/mem"
)

type vC11Pool struct {
	mu         sync.Mutex
	base       mem.BufferPool
	gets, puts int
}

func (p *vC11Pool) Get(n int) *[]byte {
	p.mu.Lock()
	p.gets++
	p.mu.Unlock()
	return p.base.Get(n)
}

func (p *vC11Pool) Put(b *[]byte) {
	p.mu.Lock()
	p.puts++
	p.mu.Unlock()
	p.base.Put(b)
}

func vC11Counts() (created, freed int64) {
	return mem.VerifyPooledWrappersCreated.Load(), mem.VerifyPooledWrappersFreed.Load()
}

// Deterministic replay of the interleaving, one step at a time.
func TestVerifyC11_FullChannelLoadAbandonsWrapper(t *testing.T) {
	pool := &vC11Pool{base: mem.DefaultBufferPool()}
	b := &recvBuffer{}
	b.initWithPool(pool)

	var want []byte
	put := func(v byte) {
		want = append(want, v)
		b.put(recvMsg{buffer: mem.SliceBuffer{v}})
	}

	put('A')                                   // writer: delivered straight to the channel
	first := <-b.c                             // reader: receives A ... and is descheduled before calling load()
	put('B')                                   // writer: channel is empty, backlog/pending empty -> refills the channel
	t.Logf("step 3: writer refilled the delivery channel before the reader's load(): len(b.c)=%d", len(b.c))
	for i := 0; i < 3000; i++ {                // writer: more tiny frames -> pooled pending chunk
		put(byte(i))
	}
	b.mu.Lock()
	t.Logf("step 4: pending chunk: len=%d cap=%d (pool-backed: %v), len(b.backlog)=%d, len(b.c)=%d", len(*b.pending), cap(*b.pending), !mem.IsBelowBufferPoolingThreshold(cap(*b.pending)), len(b.backlog), len(b.c))
	pendingBefore := b.pending
	b.mu.Unlock()

	c0, f0 := vC11Counts()
	b.load() // reader: the load() that belongs to the receive of A
	c1, f1 := vC11Counts()
	b.mu.Lock()
	t.Logf("step 5: reader's load() with a full channel: wrappers created by this call=%d, wrappers freed=%d, b.pending still owned by recvBuffer=%v, len(b.c)=%d",
		c1-c0, f1-f0, b.pending == pendingBefore, len(b.c))
	b.mu.Unlock()
	if c1-c0 == 1 && f1-f0 == 0 {
		t.Logf("C11 OBSERVED: load() constructed a pooled-buffer wrapper, took the full-channel default branch, and dropped the wrapper without Free")
	} else {
		t.Logf("C11 NOT OBSERVED: created=%d freed=%d", c1-c0, f1-f0)
	}

	// Every further load() against the still-full channel abandons one more.
	c0, _ = vC11Counts()
	for i := 0; i < 100; i++ {
		b.load()
	}
	c1, f1b := vC11Counts()
	t.Logf("100 further load() calls while the channel stays full: wrappers created=%d, freed=%d", c1-c0, f1b-f1)

	// Drain like a reader and check nothing is lost and the chunk is returned to the pool exactly once.
	c0, f0 = vC11Counts()
	got := []byte{first.buffer.ReadOnlyData()[0]}
	first.buffer.Free()
	for len(got) < len(want) {
		m := <-b.c
		b.load()
		got = append(got, m.buffer.ReadOnlyData()...)
		m.buffer.Free()
	}
	c1, f1 = vC11Counts()
	pool.mu.Lock()
	t.Logf("drain: bytes intact=%v; wrappers created=%d freed=%d; pool gets=%d puts=%d", bytes.Equal(got, want), c1-c0, f1-f0, pool.gets, pool.puts)
	pool.mu.Unlock()
}

// The same thing with a real concurrent reader (recvBufferReader) and writer:
// count how many wrappers are created vs. freed over a whole stream.
func TestVerifyC11_ConcurrentReaderWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := &vC11Pool{base: mem.DefaultBufferPool()}
	b := &recvBuffer{}
	b.initWithPool(pool)
	const n = 2_000_000
	c0, f0 := vC11Counts()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
		}
	}()
	r := recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
	for read := 0; read < n; {
		buf, err := r.Read(64)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		for _, v := range buf.ReadOnlyData() {
			if v != byte(read) {
				t.Fatalf("byte %d = %d, want %d", read, v, byte(read))
			}
			read++
		}
		buf.Free()
	}
	<-done
	c1, f1 := vC11Counts()
	pool.mu.Lock()
	t.Logf("concurrent run, %d one-byte puts fully read and freed: pooled wrappers created=%d, freed=%d, abandoned=%d; pool gets=%d puts=%d",
		n, c1-c0, f1-f0, (c1-c0)-(f1-f0), pool.gets, pool.puts)
	pool.mu.Unlock()
}
