//go:build ignore

// Audit probe for C1 (branch c70b9e5f). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
)


// Uses the branch's own newTestRecvStream(): ctx = context.Background(), so ctxDone never fires.
func TestVerifyC1Stall(t *testing.T) {
	s := newTestRecvStream()
	start := time.Now()
	t.Logf("s.read(1) with the frame/EOF withheld; intended bound defaultTestTimeout=%v", defaultTestTimeout)
	_, err := s.read(1)
	t.Logf("returned after %v err=%v", time.Since(start), err)
}
