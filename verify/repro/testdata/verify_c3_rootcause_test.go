// Run: sh verify/repro/c3_race_suite.sh  (copies this file to internal/transport/ of evalon/grpc-go-tr-eebab2d1 and runs it as step 6)

package transport

import (
	"context"
	"io"
	"strings"
	"testing"

	"google.golang.org/grpc/mem"
)

// Replays the queue/read sequence of the "enabled" case of the delivered
// TestRecvBufferCompaction and reports how many pooled buffers went back to
// the pool, once with the extra test-owned reference on the large buffer that
// the delivered test takes (large.Ref()) and once without it.
func (s) TestVerifyC3_LargeBufferPoolReturn(t *testing.T) {
	for _, extraRef := range []bool{true, false} {
		setReceiveBufferCompaction(t, true)
		pool := &countingBufferPool{}
		newPooledBuffer := func(data string) mem.Buffer {
			b := make([]byte, len(data), 4*recvBufferCompactionThreshold)
			copy(b, data)
			return mem.NewBuffer(&b, pool)
		}
		large := newPooledBuffer(strings.Repeat("L", recvBufferCompactionThreshold+1))
		readSize := 2 * large.Len()
		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
		rb, r := newTestRecvBufferReader(ctx)

		rb.put(recvMsg{buffer: mem.SliceBuffer("ab")})
		rb.put(recvMsg{buffer: mem.SliceBuffer("cd")})
		rb.put(recvMsg{buffer: newPooledBuffer("ef")})
		if extraRef {
			large.Ref()
		}
		rb.put(recvMsg{buffer: large})
		rb.put(recvMsg{buffer: mem.SliceBuffer("gh")})
		rb.put(recvMsg{err: io.EOF})
		afterQueueing := pool.puts.Load()

		for {
			got, err := r.Read(readSize)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Read() = _, %v", err)
			}
			got.Free()
		}
		afterReading := pool.puts.Load()
		t.Logf("VERIFY-C3 test-owned large.Ref()=%v: pool puts after queueing=%d, after reading and freeing everything=%d", extraRef, afterQueueing, afterReading)
		if extraRef {
			large.Free()
			t.Logf("VERIFY-C3 test-owned large.Ref()=%v: pool puts after the test also drops its own reference=%d", extraRef, pool.puts.Load())
		}
		cancel()
	}
}
