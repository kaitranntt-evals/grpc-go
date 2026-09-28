//go:build ignore

// Audit probe for C7 on branch evalon/grpc-go-tr-561ce78f. Run: go test -v -run '^TestVerifyC7' ./internal/transport -count=1
package transport

import (
	"testing"

	"google.golang.org/grpc/mem"
)

func verifyC7Retained(b *recvBuffer) (entries, capBytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.backlog {
		if m.buffer != nil {
			capBytes += cap(m.buffer.ReadOnlyData())
		}
	}
	capBytes += cap(b.tail)
	return len(b.backlog), capBytes
}

func TestVerifyC7_AlternatingTinyAnd2KiBFrames(t *testing.T) {
	pool := mem.DefaultBufferPool()
	var b recvBuffer
	b.init()
	b.put(recvMsg{buffer: mem.SliceBuffer{0}}) // occupies the channel
	// Push backlogBytes above maxCompactionChunkSize so that each new tail is sized at 16 KiB.
	b.put(recvMsg{buffer: mem.Copy(make([]byte, 20*1024), pool)})
	_, base := verifyC7Retained(&b)
	payload := 20 * 1024
	t.Logf("after 20 KiB large frame: backlogBytes=%d retainedCap=%d", b.backlogBytes, base)
	prev := base
	for pair := 1; pair <= 12; pair++ {
		b.put(recvMsg{buffer: mem.SliceBuffer{1}})
		b.put(recvMsg{buffer: mem.Copy(make([]byte, 2048), pool)})
		payload += 2049
		e, c := verifyC7Retained(&b)
		t.Logf("pair %2d: entries=%d payloadBytes=%d retainedCap=%d deltaThisPair=%d tailCap=%d tailLen=%d ratio=%.2fx",
			pair, e, payload, c, c-prev, cap(b.tail), len(b.tail), float64(c)/float64(payload))
		prev = c
	}
}
