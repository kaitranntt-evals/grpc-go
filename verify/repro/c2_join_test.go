//go:build verify_audit

// C2 repro: copy to internal/transport/ on evalon/grpc-go-tr-8b3e4b01, then: go test -tags verify_audit -v -run '^TestC2_JoinWithStalledWriter$' google.golang.org/grpc/internal/transport -count=1 -timeout 40s

package transport

// Probe for C2 on evalon/grpc-go-tr-8b3e4b01. Body is the setup + writer
// goroutine + deferred join of TestReceiveBufferCompactionConcurrent
// (internal/transport/recv_buffer_test.go:225-257), verbatim; only the reader
// loop is replaced by a simulated failure.
// Run (stalled writer; expected: hangs until the go test -timeout panic):
//   go test -tags verify_audit -v -run '^TestC2_JoinWithStalledWriter$' google.golang.org/grpc/internal/transport -count=1 -timeout 40s
// Run (control, healthy writer; expected: returns at once with the simulated failure):
//   go test -tags verify_audit -v -run '^TestC2_JoinWithHealthyWriter$' google.golang.org/grpc/internal/transport -count=1 -timeout 40s

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
)

func c2Body(t *testing.T, stallWriter bool) {
	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, true)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	var queue recvBuffer
	queue.init()
	want := make([]byte, 1<<20)
	for i := range want {
		want[i] = byte(i)
	}
	// Ensure there is a compacted backlog before starting the reader.
	for i := 0; i < 3; i++ {
		queue.put(recvMsg{buffer: mem.SliceBuffer(want[i : i+1])})
	}
	if stallWriter {
		// Simulate a writer that cannot make progress: put() blocks on b.mu.
		queue.mu.Lock()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sizes := []int{1, 7, http2MaxFrameLen, 2, 4096}
		for offset, frame := 3, 0; offset < len(want); frame++ {
			if ctx.Err() != nil {
				return
			}
			end := min(offset+sizes[frame%len(sizes)], len(want))
			queue.put(recvMsg{buffer: mem.Copy(want[offset:end], mem.DefaultBufferPool())})
			offset = end
		}
		queue.put(recvMsg{err: io.EOF})
	}()
	start := time.Now()
	defer func() {
		cancel()
		t.Logf("C2: cancel() called at +%v; now waiting on <-done (stallWriter=%v)", time.Since(start).Round(time.Millisecond), stallWriter)
		<-done
		t.Logf("C2: join returned at +%v", time.Since(start).Round(time.Millisecond))
	}()

	time.Sleep(100 * time.Millisecond)
	t.Fatal("C2: simulated reader-side failure (stands in for any t.Fatal in the read loop)")
}

func TestC2_JoinWithStalledWriter(t *testing.T) { c2Body(t, true) }
func TestC2_JoinWithHealthyWriter(t *testing.T) { c2Body(t, false) }
