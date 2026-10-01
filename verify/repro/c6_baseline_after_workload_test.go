//go:build verify

// Run: bash verify/run.sh C6   (copies this file into internal/transport of a worktree of evalon/grpc-go-tr-91caeccb; runs it and the branch's own TestClientStream_TinyDataFramesMemory at GOMAXPROCS=1 and default, then again with repro/c6_disable_compaction_mutation.patch applied)

package transport

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

// verifyC6Measure is the branch's measureClientTinyDataFrames, statement for
// statement, using the branch's own startTinyDataFramesServer,
// heapAllocAfterGC and heapGrowth. The only additions are the two
// received() samples, which read how many DATA payload bytes the client
// transport has already queued on the stream (inFlow.pendingData) around the
// baseline sample. They add no synchronization between producer and baseline.
func verifyC6Measure(t *testing.T, numFrames int) (growth uint64, rxBeforeBaseline, rxAtBaseline uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	lis, pingAcked := startTinyDataFramesServer(t, numFrames)
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatalf("NewHTTP2Client failed: %v", err)
	}
	defer ct.Close(errors.New("test done"))
	stream, err := ct.NewStream(ctx, &CallHdr{}, nil)
	if err != nil {
		t.Fatalf("NewStream failed: %v", err)
	}
	received := func() uint32 {
		stream.fc.mu.Lock()
		defer stream.fc.mu.Unlock()
		return stream.fc.pendingData
	}

	rxBeforeBaseline = received() // added
	before := heapAllocAfterGC()
	rxAtBaseline = received() // added
	select {
	case <-pingAcked:
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for client to receive all DATA frames")
	}
	after := heapAllocAfterGC()

	data, err := stream.Read(numFrames)
	if err != nil {
		t.Fatalf("stream.Read(%d) failed: %v", numFrames, err)
	}
	got := data.Materialize()
	data.Free()
	if want := tinyPayload(numFrames); !bytes.Equal(got, want) {
		t.Fatalf("stream.Read(%d) returned unexpected payload", numFrames)
	}
	return heapGrowth(before, after), rxBeforeBaseline, rxAtBaseline
}

// TestVerifyC6_BaselineVsWorkload replays the branch's
// TestClientStream_TinyDataFramesMemory, including its exact final assertion,
// and logs how much of the measured DATA workload had already been received
// (and therefore had its receive buffers allocated) when the baseline heap
// sample returned.
func (s) TestVerifyC6_BaselineVsWorkload(t *testing.T) {
	const numFrames = defaultWindowSize

	var compacted, uncompacted uint64
	t.Run("compaction_enabled", func(t *testing.T) {
		testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, true)
		var a, b uint32
		compacted, a, b = verifyC6Measure(t, numFrames)
		t.Logf("BASELINE compaction=true:  DATA bytes already received when baseline sampling started=%d, when it returned=%d of %d; reported growth=%d", a, b, numFrames, compacted)
	})
	t.Run("compaction_disabled", func(t *testing.T) {
		testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, false)
		var a, b uint32
		uncompacted, a, b = verifyC6Measure(t, numFrames)
		t.Logf("BASELINE compaction=false: DATA bytes already received when baseline sampling started=%d, when it returned=%d of %d; reported growth=%d", a, b, numFrames, uncompacted)
	})
	// The branch's assertion, verbatim.
	rejected := compacted*4 > uncompacted
	t.Logf("ASSERTION compacted=%d uncompacted=%d: branch check `compacted*4 > uncompacted` is %v => comparison %s", compacted, uncompacted, rejected, map[bool]string{true: "REJECTED", false: "ACCEPTED as a >=4x reduction"}[rejected])
}

// TestVerifyC6_ZeroVersusZero feeds the branch's helpers the zero-delta case
// directly: negative heap deltas are clamped to zero by heapGrowth, and the
// branch's assertion expression is evaluated on the result.
func (s) TestVerifyC6_ZeroVersusZero(t *testing.T) {
	compacted := heapGrowth(5_000_000, 4_900_000)   // heap shrank by 100 kB
	uncompacted := heapGrowth(9_000_000, 5_000_000) // heap shrank by 4 MB
	t.Logf("heapGrowth(5000000, 4900000)=%d heapGrowth(9000000, 5000000)=%d", compacted, uncompacted)
	rejected := compacted*4 > uncompacted // the branch's assertion, verbatim
	t.Logf("ASSERTION compacted=%d uncompacted=%d: branch check `compacted*4 > uncompacted` is %v => comparison %s", compacted, uncompacted, rejected, map[bool]string{true: "REJECTED", false: "ACCEPTED as a >=4x reduction"}[rejected])
}
