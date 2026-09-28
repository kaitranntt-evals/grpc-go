//go:build ignore

// Audit probe for C1 (branch cc649db0). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
	"google.golang.org/grpc/mem"
)


// Mirrors TestReceiveBufferCompactionChunkStorage drain loop: `for range len(queue.backlog)+len(queue.c) { m := <-queue.get(); queue.load() }`.
func TestVerifyC1Stall(t *testing.T) {
	var queue recvBuffer
	queue.init()
	queue.put(recvMsg{buffer: mem.SliceBuffer{1}})
	n := len(queue.backlog) + len(queue.c) + 1 // one receive more than delivered (simulated miscount / lost message)
	start := time.Now()
	for i := 0; i < n; i++ {
		t.Logf("receive %d", i)
		m := <-queue.get()
		queue.load()
		m.buffer.Free()
	}
	t.Logf("drained after %v", time.Since(start))
}
