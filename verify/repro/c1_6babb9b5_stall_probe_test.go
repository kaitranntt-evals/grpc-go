//go:build ignore

// Audit probe for C1 (branch 6babb9b5). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
	"google.golang.org/grpc/mem"
)


// Mirrors TestReceiveBufferCompaction_BufferOwnership/_PoolAndErrors: `msg := <-b.get()` and `r := recvBufferReader{recv: &b}` (no ctx).
func TestVerifyC1Stall(t *testing.T) {
	var b recvBuffer
	b.init()
	b.put(recvMsg{buffer: mem.SliceBuffer{1}})
	msg := <-b.get()
	msg.buffer.Free()
	b.load()
	r := recvBufferReader{recv: &b}
	start := time.Now()
	t.Logf("r.Read(100) with the next frame/EOF withheld; intended bound defaultTestTimeout=%v", defaultTestTimeout)
	_, err := r.Read(100)
	t.Logf("returned after %v err=%v", time.Since(start), err)
}
