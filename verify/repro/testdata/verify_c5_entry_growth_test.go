// Run: sh verify/repro/c5_entry_growth.sh  (copies this file to internal/transport/ of evalon/grpc-go-tr-92749eaf and runs it)

package transport

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"google.golang.org/grpc/mem"
)

// Measures how many queue entries (channel + backlog + pending compaction
// buffer) a recvBuffer holds after N one-byte buffers are queued behind a
// reader that is not reading, for doubling N. The comment on compactLocked
// says the number of queued entries "stays logarithmic in the number of
// compacted bytes"; a logarithmic count grows by a constant when N doubles,
// a linear count doubles.
func (s) TestVerifyC5_QueueEntryGrowth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	for _, tc := range []struct {
		name string
		pool mem.BufferPool
	}{
		// What the http2 client/server transports install (s.Stream.buf.pool = t.bufferPool).
		{name: "default pool", pool: mem.DefaultBufferPool()},
		// A pool that returns exactly-sized buffers, to show the result does
		// not depend on the default pool rounding capacities up.
		{name: "exact-size pool", pool: mem.NopBufferPool{}},
	} {
		t.Logf("VERIFY-C5 [%s] maxCompactionBufferSize=%d minCompactionBufferSize=%d", tc.name, maxCompactionBufferSize, minCompactionBufferSize)
		t.Logf("VERIFY-C5 [%s] %10s %8s %8s %10s %14s  %s", tc.name, "bytes(N)", "entries", "log2(N)", "N/16384", "entries(N)-entries(N/2)", "capacities of first entries")
		prev := 0
		for exp := 10; exp <= 23; exp++ {
			n := 1 << exp
			var b recvBuffer
			b.init()
			b.pool = tc.pool
			for i := 0; i < n; i++ {
				b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
			}

			b.mu.Lock()
			entries := len(b.c) + len(b.backlog)
			var caps []string
			for i, m := range b.backlog {
				if i < 8 {
					caps = append(caps, fmt.Sprint(cap(m.buffer.ReadOnlyData())))
				}
			}
			if b.pending != nil {
				entries++
			}
			b.mu.Unlock()
			t.Logf("VERIFY-C5 [%s] %10d %8d %8.0f %10d %14d  [%s ...]", tc.name, n, entries, math.Log2(float64(n)), n/maxCompactionBufferSize, entries-prev, strings.Join(caps, " "))
			prev = entries

			// Drain so that every pooled compaction buffer is returned.
			b.put(recvMsg{err: context.Canceled})
			if got, err := readAll(ctx, &b); err != context.Canceled || len(got) != n {
				t.Fatalf("readAll() = %d bytes, %v; want %d bytes, %v", len(got), err, n, context.Canceled)
			}
		}
	}
}
