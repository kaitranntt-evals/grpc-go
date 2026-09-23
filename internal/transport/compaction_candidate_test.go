package transport

import (
	"testing"
	"google.golang.org/grpc/mem"
)

func (s) TestCandidateCompaction(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &recvBuffer{}
	b.init(pool)

	for range 2000 {
		b.put(recvMsg{buffer: mem.Copy([]byte{0x01}, pool)})
	}
	if len(b.backlog) > 1000 {
		t.Fatalf("backlog not compacted: %d", len(b.backlog))
	}
}
