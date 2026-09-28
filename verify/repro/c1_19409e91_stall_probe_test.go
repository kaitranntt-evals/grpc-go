//go:build ignore

// Audit probe for C1 (branch 19409e91). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
)


// Uses the branch's own newRecvBufferTestStream(): recvBufferReader{recv: &s.buf} with no ctx.
func TestVerifyC1Stall(t *testing.T) {
	st := newRecvBufferTestStream()
	start := time.Now()
	t.Logf("st.readAll(1) with the frame withheld; intended bound defaultTestTimeout=%v", defaultTestTimeout)
	_, err := st.readAll(1)
	t.Logf("returned after %v err=%v", time.Since(start), err)
}
