//go:build ignore

// Audit probe for C1 (branch f547726c). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
)


// Mirrors TestReceiveBufferCompactionDataFrames: `m := <-stream.buf.c` (bare receive on the recvBuffer channel, i == 0 branch).
func TestVerifyC1Stall(t *testing.T) {
	var stream Stream
	stream.buf.init()
	start := time.Now()
	t.Logf("<-stream.buf.c with the first frame not delivered to the channel (simulated regression); intended bound defaultTestTimeout=%v", defaultTestTimeout)
	m := <-stream.buf.c
	t.Logf("returned after %v: %v", time.Since(start), m)
}
