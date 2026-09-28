//go:build ignore

// Audit probe for C1 (branch 36805c4b). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
	"google.golang.org/grpc/mem"
)


// Mirrors TestRecvBufferCompactionDoesNotRecopy drain loop: `for i := 0; i < entries+1; i++ { m := <-queue.get(); queue.load() }`.
func TestVerifyC1Stall(t *testing.T) {
	var queue recvBuffer
	queue.init()
	queue.put(recvMsg{buffer: mem.SliceBuffer{1}})
	entries := 1 // one more receive than messages actually delivered (simulated miscount / lost message)
	start := time.Now()
	for i := 0; i < entries+1; i++ {
		t.Logf("receive %d", i)
		m := <-queue.get()
		queue.load()
		m.buffer.Free()
	}
	t.Logf("drained after %v", time.Since(start))
}
