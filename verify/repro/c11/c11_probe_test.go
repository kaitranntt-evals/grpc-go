//go:build c11probe

// Run from the repo root: cp verify/repro/c11/c11_probe_test.go internal/transport/ && go test -tags c11probe -run '^TestC11Probe$' ./internal/transport -count=1 -v; rm internal/transport/c11_probe_test.go

package transport

import (
	"fmt"
	"testing"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
)

// C11 probe: queue N one-byte buffers (plus one to occupy the channel), then
// a fully used 16 KiB pooled buffer, and report whether the large buffer's
// backing array survived put().
func TestC11Probe(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, true)
	fmt.Printf("recvMsgSize=%d compactionThreshold=%d utilizationFactor=%d\n", recvMsgSize, compactionThreshold, utilizationFactor)
	for _, n := range []int{1023, 1024} {
		var b recvBuffer
		pool := mem.DefaultBufferPool()
		b.init(pool)
		b.put(recvMsg{buffer: mem.SliceBuffer{0}}) // handed straight to the channel
		for i := 0; i < n; i++ {
			b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
		}
		b.mu.Lock()
		fmt.Printf("n=%d: before large put: len(backlog)=%d uncompactedSuffixLen=%d uncompactedBytes=%d heapEstimate=%d\n",
			n, len(b.backlog), b.uncompactedSuffixLen, b.uncompactedBytes, b.uncompactedSuffixLen*recvMsgSize+b.uncompactedBytes)
		b.mu.Unlock()

		const largeLen = 16 * 1024
		raw := pool.Get(largeLen)
		for i := range *raw {
			(*raw)[i] = 0xAB
		}
		large := mem.NewBuffer(raw, pool)
		data := large.ReadOnlyData()
		fmt.Printf("n=%d: large buffer len=%d cap=%d belowPoolingThreshold=%v\n", n, len(data), cap(data), mem.IsBelowBufferPoolingThreshold(len(data)))
		before := &data[0]

		b.put(recvMsg{buffer: large})

		b.mu.Lock()
		last := b.backlog[len(b.backlog)-1].buffer
		sameArray := &last.ReadOnlyData()[0] == before
		fmt.Printf("n=%d: after large put: len(backlog)=%d lastEntryLen=%d lastEntrySharesBackingArray=%v uncompactedSuffixLen=%d uncompactedBytes=%d\n",
			n, len(b.backlog), last.Len(), sameArray, b.uncompactedSuffixLen, b.uncompactedBytes)
		b.mu.Unlock()
		if n == 1024 && !sameArray {
			t.Errorf("n=%d: the 16 KiB buffer was copied during compaction (len(backlog)=%d)", n, len(b.backlog))
		}
	}
}
