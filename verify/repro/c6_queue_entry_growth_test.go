// Run (after verify/repro/setup_worktrees.sh): cp verify/repro/c6_queue_entry_growth_test.go /tmp/claims/6fe210bc/internal/transport/ && (cd /tmp/claims/6fe210bc && go test ./internal/transport -run '^TestVerifyC6' -count=1 -v); rm /tmp/claims/6fe210bc/internal/transport/c6_queue_entry_growth_test.go
package transport

import (
	"math/bits"
	"testing"

	"google.golang.org/grpc/mem"
)

// TestVerifyC6_QueueEntryGrowth queues one-byte payloads with no consumption
// and records the number of queue entries (backlog entries plus the chunk
// being filled) each time the queued byte count doubles.
func TestVerifyC6_QueueEntryGrowth(t *testing.T) {
	for _, pool := range []mem.BufferPool{nil, mem.DefaultBufferPool()} {
		var b recvBuffer
		b.init(pool)
		entries := func() int {
			n := len(b.backlog)
			if b.chunk != nil {
				n++
			}
			return n
		}
		const total = 8 << 20
		t.Logf("pool=%T recvBufferMaxChunkSize=%d (http2MaxFrameLen=%d)", pool, recvBufferMaxChunkSize, http2MaxFrameLen)
		t.Logf("%10s %8s %10s %12s %14s", "queuedB", "entries", "log2(B)", "B/maxChunk", "delta-entries")
		prev := 0
		got := map[int]int{}
		for i := 1; i <= total+1; i++ {
			b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
			q := i - 1 // the first payload sits in the channel, not the queue
			if q >= 1024 && q&(q-1) == 0 {
				e := entries()
				got[q] = e
				t.Logf("%10d %8d %10d %12d %14d", q, e, bits.Len(uint(q))-1, q/recvBufferMaxChunkSize, e-prev)
				prev = e
			}
		}
		// Beyond the chunk-capacity cap every doubling of queued bytes must add
		// about as many entries as there were bytes added / cap, i.e. linear growth.
		for q := 4 * recvBufferMaxChunkSize; q < total; q *= 2 {
			added := got[2*q] - got[q]
			if want := q / recvBufferMaxChunkSize; added != want {
				t.Errorf("queued %d -> %d bytes added %d entries, want %d (linear)", q, 2*q, added, want)
			}
		}
		// Logarithmic growth would keep the 8 MiB entry count near log2(8 MiB) = 23.
		if e := got[total]; e < total/recvBufferMaxChunkSize {
			t.Errorf("entries at %d queued bytes = %d, want >= %d", total, e, total/recvBufferMaxChunkSize)
		}
		<-b.c
		for _, m := range b.backlog {
			m.buffer.Free()
		}
	}
}
