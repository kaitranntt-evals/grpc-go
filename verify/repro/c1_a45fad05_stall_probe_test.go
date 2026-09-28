//go:build ignore

// Audit probe for C1 (branch a45fad05). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"testing"
	"time"
)


// Mirrors TestStreamReadManySmallFrames: Stream with recvBufferReader{recv: &s.buf} and no ctx.
func TestVerifyC1Stall(t *testing.T) {
	s := Stream{readRequester: &fakeReadRequester{}}
	s.buf.init()
	s.trReader = transportReader{reader: recvBufferReader{recv: &s.buf}, windowHandler: &mockWindowUpdater{f: func(int) {}}}
	start := time.Now()
	t.Logf("s.readTo(1 byte) with the frame withheld; intended bound defaultTestTimeout=%v", defaultTestTimeout)
	_, err := s.readTo(make([]byte, 1))
	t.Logf("returned after %v err=%v", time.Since(start), err)
}
