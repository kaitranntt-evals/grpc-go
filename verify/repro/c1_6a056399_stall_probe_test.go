//go:build ignore

// Audit probe for C1 (branch 6a056399). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
)


// Uses the branch's own newTestRecvBufferStream() helper: recvBufferReader{recv: &s.buf} with no ctx.
func TestVerifyC1Stall(t *testing.T) {
	s := newTestRecvBufferStream()
	start := time.Now()
	t.Logf("s.readTo(1 byte) with the frame withheld (simulated lost frame/EOF); intended bound defaultTestTimeout=%v", defaultTestTimeout)
	_, err := s.readTo(make([]byte, 1))
	t.Logf("returned after %v err=%v", time.Since(start), err)
}
