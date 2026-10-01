//go:build verify

// Run: bash verify/run.sh C5   (copies this file + verify_helpers_test.go into internal/transport of a worktree of evalon/grpc-go-tr-c63d3773 and runs go test -tags verify -run 'Test/VerifyC5')

package transport

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

func verifyC5Report(t *testing.T, b *recvBuffer, pool *verifyTrackingPool) (maxReq int) {
	t.Helper()
	gets, liveCount, liveBytes := pool.recvBufferGets()
	var reqs []string
	for _, g := range gets {
		reqs = append(reqs, fmt.Sprintf("Get(%d)->cap %d", g.req, g.cap))
		maxReq = max(maxReq, g.req)
	}
	b.mu.Lock()
	chunkCap, chunkLen := 0, b.chunkLen
	if b.chunk != nil {
		chunkCap = cap(*b.chunk)
	}
	backlog := len(b.backlog)
	b.mu.Unlock()
	t.Logf("  before any read/flush: backlog entries=%d, unflushed compaction chunk: cap=%d bytes holding %d payload byte(s)", backlog, chunkCap, chunkLen)
	t.Logf("  pool.Get calls made by recvBuffer: %v; still outstanding: %d buffers, %d bytes", reqs, liveCount, liveBytes)
	return maxReq
}

// Puts a burst of two / three one-byte payloads on a recvBuffer that is not
// being read, initialised exactly as production does (init(pool)).
func (s) TestVerifyC5_UnitShortBursts(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, burst := range []int{2, 3} {
			t.Run(fmt.Sprintf("compaction=%v/burst=%d", enabled, burst), func(t *testing.T) {
				testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
				pool := newVerifyTrackingPool()
				var rb recvBuffer
				rb.init(pool)
				for i := 0; i < burst; i++ {
					rb.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
				}
				t.Logf("queued a burst of %d one-byte payloads:", burst)
				maxReq := verifyC5Report(t, &rb, pool)
				t.Logf("RESULT compaction=%v burst=%d: largest compaction destination requested = %d bytes (> 1024: %v)", enabled, burst, maxReq, maxReq > 1024)

				ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
				defer cancel()
				r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &rb}
				var got []byte
				for len(got) < burst {
					buf, err := r.Read(1024)
					if err != nil {
						t.Fatalf("Read: %v", err)
					}
					got = append(got, buf.ReadOnlyData()...)
					buf.Free()
				}
				verifyCheckPayload(t, got, burst)
				_, liveCount, liveBytes := pool.recvBufferGets()
				t.Logf("  after delivering all %d bytes in order: outstanding recvBuffer pool buffers: %d (%d bytes)", burst, liveCount, liveBytes)
			})
		}
	}
}

// Same bursts through production code only: a real http2Client receives two /
// three one-byte DATA frames while the application is not reading.
func (s) TestVerifyC5_ClientShortBursts(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, burst := range []int{2, 3} {
			t.Run(fmt.Sprintf("compaction=%v/burst=%d", enabled, burst), func(t *testing.T) {
				testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
				sizes := make([]int, burst)
				for i := range sizes {
					sizes[i] = 1
				}
				addr, send, delivered := verifyStartRawServer(t, sizes)
				ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
				defer cancel()
				pool := newVerifyTrackingPool()
				ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: addr}, ConnectOptions{BufferPool: pool}, func(GoAwayInfo) {})
				if err != nil {
					t.Fatalf("NewHTTP2Client: %v", err)
				}
				defer ct.Close(errors.New("test done"))
				cs, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "foo.Bar"}, nil)
				if err != nil {
					t.Fatalf("NewStream: %v", err)
				}
				if _, err := cs.Header(); err != nil {
					t.Fatalf("Header: %v", err)
				}
				close(send)
				select {
				case <-delivered:
				case <-ctx.Done():
					t.Fatal("timed out waiting for DATA frames")
				}
				t.Logf("client received a burst of %d one-byte DATA frames:", burst)
				maxReq := verifyC5Report(t, &cs.buf, pool)
				t.Logf("RESULT compaction=%v burst=%d: largest compaction destination requested = %d bytes (> 1024: %v)", enabled, burst, maxReq, maxReq > 1024)

				data, err := cs.Read(burst)
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				got := data.Materialize()
				data.Free()
				verifyCheckPayload(t, got, burst)
			})
		}
	}
}
