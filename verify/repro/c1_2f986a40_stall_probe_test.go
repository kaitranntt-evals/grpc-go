//go:build ignore

// Audit probe for C1 (branch 2f986a40). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"io"
	"testing"
	"time"
	"google.golang.org/grpc/mem"
)


// Mirrors TestReceiveBufferCompactionConcurrent: `defer func() { <-done }()` joining a writer goroutine that only calls b.put.
// Failure path: the reader bails out immediately (as t.Fatal would) and the deferred join runs.
func TestVerifyC1Join(t *testing.T) {
	var b recvBuffer
	b.init()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 64*1024; i++ {
			b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
		}
		b.put(recvMsg{err: io.EOF})
	}()
	start := time.Now()
	<-done
	t.Logf("join <-done returned after %v (writer only performs non-blocking put calls; nothing a stalled reader can block)", time.Since(start))
}
