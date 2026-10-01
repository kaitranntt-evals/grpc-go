//go:build verify

// Run: bash verify/run.sh C3   (copies this file + verify_helpers_test.go into internal/transport of a worktree of evalon/grpc-go-tr-8d87df46 and runs go test -tags verify -run 'Test/VerifyC3')

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

// Queues homogeneous 4 KiB frames, each in its own exact-size (cap 4096)
// buffer from the default pool's 4 KiB tier, on a recvBuffer that is not being
// read, and reports which backing arrays survive to the backlog and to
// delivery.
func (s) TestVerifyC3_Unit4KiBFrames(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction=%v", enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableRecvBufferCompaction, enabled)
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()
			pool := newVerifyTrackingPool()
			var rb recvBuffer
			rb.init()
			rb.pool = pool // exactly what http2Client.newStream / http2Server.operateHeaders do

			const frames = 9
			orig := map[*byte]int{} // backing array -> frame index
			j := 0
			for i := 0; i < frames; i++ {
				h := pool.Get(4096)
				if cap(*h) != 4096 {
					t.Fatalf("frame %d: pool.Get(4096) returned cap %d, want the exact-size 4096 tier", i, cap(*h))
				}
				for k := range *h {
					(*h)[k] = byte(j)
					j++
				}
				orig[&(*h)[0]] = i
				rb.put(recvMsg{buffer: mem.NewBuffer(h, pool)})
			}

			rb.mu.Lock()
			var desc []string
			for _, m := range rb.backlog {
				d := m.buffer.ReadOnlyData()
				_, isOrig := orig[&d[0]]
				desc = append(desc, fmt.Sprintf("{len=%d cap=%d original=%v}", len(d), cap(d), isOrig))
			}
			rb.mu.Unlock()
			pool.mu.Lock()
			origLive := 0
			for p := range pool.live {
				if _, ok := orig[p]; ok {
					origLive++
				}
			}
			pool.mu.Unlock()
			gets, _, _ := pool.recvBufferGets()
			var reqs []int
			for _, g := range gets {
				reqs = append(reqs, g.req)
			}
			t.Logf("queued %d x 4096-byte frames (each cap 4096 from the default pool), unread:", frames)
			t.Logf("  backlog entries: %v", desc)
			t.Logf("  original 4 KiB buffers still held (not returned to the pool): %d of %d", origLive, frames)
			t.Logf("  pool.Get calls made by recvBuffer itself: %v", reqs)

			r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &rb}
			var got []byte
			deliveredOrig, deliveredCopies := 0, 0
			for len(got) < frames*4096 {
				buf, err := r.Read(1 << 20)
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				d := buf.ReadOnlyData()
				if _, ok := orig[&d[0]]; ok {
					deliveredOrig++
				} else {
					deliveredCopies++
				}
				got = append(got, d...)
				buf.Free()
			}
			verifyCheckPayload(t, got, frames*4096)
			t.Logf("  delivered: %d buffers with original backing storage, %d buffers with copied storage; all %d bytes in order", deliveredOrig, deliveredCopies, len(got))
			copied := frames - origLive
			t.Logf("RESULT compaction=%v: %d of %d queued 4 KiB frames had their storage replaced by a copy", enabled, copied, frames)
		})
	}
}

// Same observation through production code only: a real http2Client receives
// 4 KiB DATA frames from a raw HTTP/2 server while the application is not
// reading. The framer takes each frame's buffer from the transport's pool.
func (s) TestVerifyC3_Client4KiBFrames(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction=%v", enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableRecvBufferCompaction, enabled)
			const frames = 9 // 36 KiB, inside the 64 KiB default stream window
			sizes := make([]int, frames)
			for i := range sizes {
				sizes[i] = 4096
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

			pool.mu.Lock()
			framerGets, framerLive := 0, 0
			for _, g := range pool.gets {
				if g.req == 4096 && !g.fromRecvBuffer {
					framerGets++
					if _, ok := pool.live[g.ptr]; ok {
						framerLive++
					}
				}
			}
			pool.mu.Unlock()
			gets, liveCount, liveBytes := pool.recvBufferGets()
			var reqs []int
			for _, g := range gets {
				reqs = append(reqs, g.req)
			}
			cs.buf.mu.Lock()
			var lens []int
			for _, m := range cs.buf.backlog {
				if m.buffer != nil {
					lens = append(lens, m.buffer.Len())
				} else if cs.buf.tail != nil {
					lens = append(lens, -len(*cs.buf.tail)) // unsealed tail
				}
			}
			cs.buf.mu.Unlock()
			t.Logf("client received %d x 4096-byte DATA frames, unread:", frames)
			t.Logf("  framer took %d exact-size 4096 buffers from the pool; %d still held by the stream", framerGets, framerLive)
			t.Logf("  recvBuffer backlog entry lengths (negative = unsealed compaction tail): %v", lens)
			t.Logf("  pool.Get calls made by recvBuffer itself: %v (still outstanding: %d buffers, %d bytes)", reqs, liveCount, liveBytes)

			data, err := cs.Read(frames * 4096)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got := data.Materialize()
			data.Free()
			verifyCheckPayload(t, got, frames*4096)
			t.Logf("RESULT compaction=%v: %d of %d received 4 KiB frames were copied out of their pool buffer; all %d bytes delivered in order", enabled, framerGets-framerLive, framerGets, len(got))
		})
	}
}
