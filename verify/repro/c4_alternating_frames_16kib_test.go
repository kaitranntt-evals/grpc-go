//go:build verify

// Run: bash verify/run.sh C4   (copies this file + verify_helpers_test.go into internal/transport of a worktree of evalon/grpc-go-tr-53b22674 and runs go test -tags verify -run 'Test/VerifyC4')

package transport

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"unsafe"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

// verifyC4Summarize reports, for every one-byte backlog entry (an isolated
// one-byte payload), the capacity and identity of its backing array.
func verifyC4Summarize(t *testing.T, b *recvBuffer, warmup int) (oneByte, atLeast16K, distinct int) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[*byte]bool{}
	var caps []int
	var entryLens = map[int]int{}
	for _, m := range b.backlog {
		if m.buffer == nil {
			continue
		}
		d := m.buffer.ReadOnlyData()
		entryLens[len(d)]++
		if len(d) != 1 {
			continue
		}
		oneByte++
		caps = append(caps, cap(d))
		if oneByte <= warmup {
			continue
		}
		if cap(d) >= 16*1024 {
			atLeast16K++
		}
		if p := unsafe.SliceData(d); !seen[p] {
			seen[p] = true
			distinct++
		}
	}
	if b.pending != nil && len(*b.pending) == 1 {
		oneByte++
		caps = append(caps, cap(*b.pending))
		if cap(*b.pending) >= 16*1024 {
			atLeast16K++
		}
		if p := unsafe.SliceData(*b.pending); !seen[p] {
			seen[p] = true
			distinct++
		}
	}
	var lens []int
	for l := range entryLens {
		lens = append(lens, l)
	}
	sort.Ints(lens)
	for _, l := range lens {
		t.Logf("  backlog entries of length %d: %d", l, entryLens[l])
	}
	head := caps
	if len(head) > 12 {
		head = head[:12]
	}
	t.Logf("  backing capacity of the one-byte entries, in arrival order (first %d of %d): %v", len(head), len(caps), head)
	return oneByte, atLeast16K, distinct
}

// Alternates one-byte and 2 KiB payloads on a recvBuffer that is not being
// read, initialised exactly as production does (init(pool)), once with the
// default pool and once with a nil pool.
func (s) TestVerifyC4_UnitAlternating(t *testing.T) {
	const pairs = 200
	const warmup = 8 // one-byte entries ignored as allocation warm-up
	for _, enabled := range []bool{true, false} {
		for _, poolName := range []string{"default", "nil"} {
			t.Run(fmt.Sprintf("compaction=%v/pool=%s", enabled, poolName), func(t *testing.T) {
				testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
				var tp *verifyTrackingPool
				var pool mem.BufferPool
				if poolName == "default" {
					tp = newVerifyTrackingPool()
					pool = tp
				}
				rb := new(recvBuffer)
				before := verifyLiveHeap()
				rb.init(pool)
				payload := 0
				j := 0
				next := func(n int) mem.Buffer {
					d := make([]byte, n)
					for i := range d {
						d[i] = byte(j)
						j++
					}
					payload += n
					return mem.SliceBuffer(d)
				}
				rb.put(recvMsg{buffer: next(2048)}) // taken by the channel; everything below is queued
				for i := 0; i < pairs; i++ {
					rb.put(recvMsg{buffer: next(1)})
					rb.put(recvMsg{buffer: next(2048)})
				}
				retained := verifyLiveHeap() - before
				t.Logf("queued 2048 + %d x (1 byte, 2048 bytes) = %d payload bytes, unread:", pairs, payload)
				oneByte, big, distinct := verifyC4Summarize(t, rb, warmup)
				if tp != nil {
					gets, liveCount, liveBytes := tp.recvBufferGets()
					hist := map[int]int{}
					for _, g := range gets {
						hist[g.req]++
					}
					t.Logf("  pool.Get calls made by recvBuffer (request size -> count): %v; still outstanding: %d buffers, %d bytes", hist, liveCount, liveBytes)
				}
				t.Logf("  live heap retained by the queue: %d bytes = %.1fx the unread payload", retained, float64(retained)/float64(payload))
				t.Logf("RESULT compaction=%v pool=%s: of %d one-byte payloads after a %d-payload warm-up, %d sit alone in a backing array of >= 16384 bytes (%d distinct arrays)", enabled, poolName, oneByte-warmup, warmup, big, distinct)

				ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
				defer cancel()
				r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: rb}
				var got []byte
				for len(got) < payload {
					buf, err := r.Read(1 << 20)
					if err != nil {
						t.Fatalf("Read: %v", err)
					}
					got = append(got, buf.ReadOnlyData()...)
					buf.Free()
				}
				verifyCheckPayload(t, got, payload)
				runtime.KeepAlive(rb)
			})
		}
	}
}

// Same workload through production code only: a real http2Client receives
// alternating 1-byte / 2048-byte DATA frames (inside the default 64 KiB
// stream window) while the application is not reading.
func (s) TestVerifyC4_ClientAlternating(t *testing.T) {
	const pairs = 30
	const warmup = 8
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction=%v", enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
			sizes := []int{2048}
			payload := 2048
			for i := 0; i < pairs; i++ {
				sizes = append(sizes, 1, 2048)
				payload += 2049
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
			t.Logf("client received 2048 + %d x (1 byte, 2048 bytes) = %d payload bytes of DATA frames, unread:", pairs, payload)
			oneByte, big, distinct := verifyC4Summarize(t, &cs.buf, warmup)
			gets, liveCount, liveBytes := pool.recvBufferGets()
			hist := map[int]int{}
			for _, g := range gets {
				hist[g.req]++
			}
			t.Logf("  pool.Get calls made by recvBuffer (request size -> count): %v; still outstanding: %d buffers, %d bytes", hist, liveCount, liveBytes)
			t.Logf("RESULT compaction=%v: of %d one-byte payloads after a %d-payload warm-up, %d sit alone in a backing array of >= 16384 bytes (%d distinct arrays); compaction buffers pin %d bytes for %d unread payload bytes", enabled, oneByte-warmup, warmup, big, distinct, liveBytes, payload)

			data, err := cs.Read(payload)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got := data.Materialize()
			data.Free()
			verifyCheckPayload(t, got, payload)
		})
	}
}
