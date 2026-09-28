//go:build ignore

// Audit probe for C1 (branch 63dff0f9). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
	"google.golang.org/grpc/mem"
)


// Mirrors TestReceiveBufferCompactionErrors/Pool: reader := recvBufferReader{recv: &queue} (no ctx/ctxDone) and bare `msg := <-queue.get()` loops.
func TestVerifyC1Stall(t *testing.T) {
	var queue recvBuffer
	queue.init()
	queue.put(recvMsg{buffer: mem.SliceBuffer{1}})
	reader := recvBufferReader{recv: &queue}
	b, err := reader.Read(1)
	t.Logf("first Read: len=%d err=%v", b.Len(), err)
	b.Free()
	start := time.Now()
	t.Logf("second Read with the next message withheld (simulated lost frame/EOF); test's intended bound is defaultTestTimeout=%v", defaultTestTimeout)
	_, err = reader.Read(1)
	t.Logf("returned after %v err=%v", time.Since(start), err)
}
