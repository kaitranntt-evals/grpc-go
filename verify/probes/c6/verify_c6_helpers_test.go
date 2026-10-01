// C6 instrumentation (copied to internal/transport/ by c6_apply.py): a counting
// buffer pool, and logging around the data.Free() in the Stream.readTo test helper.
package transport

import (
	"fmt"
	"os"
	"sync/atomic"

	"google.golang.org/grpc/mem"
)

type verifyC6CountingPool struct {
	base       mem.BufferPool
	gets, puts atomic.Int64
}

func (p *verifyC6CountingPool) Get(n int) *[]byte { p.gets.Add(1); return p.base.Get(n) }
func (p *verifyC6CountingPool) Put(b *[]byte)     { p.puts.Add(1); p.base.Put(b) }

// verifyC6Pool stands in for mem.DefaultBufferPool() in the patched tests.
var verifyC6Pool = &verifyC6CountingPool{base: mem.DefaultBufferPool()}

type verifyC6Before struct {
	bufs, pooled, bytes int
	puts                int64
}

// verifyC6BeforeFree describes the delivered buffers readTo is about to Free.
func verifyC6BeforeFree(data mem.BufferSlice) verifyC6Before {
	b := verifyC6Before{puts: verifyC6Pool.puts.Load()}
	for _, buf := range data {
		b.bufs++
		b.bytes += buf.Len()
		if _, plain := buf.(mem.SliceBuffer); !plain {
			b.pooled++ // ref-counted buffer that returns its storage to a pool on Free
		}
	}
	return b
}

// verifyC6AfterFree runs after readTo's deferred data.Free().
func verifyC6AfterFree(b verifyC6Before) {
	fmt.Fprintf(os.Stderr, "VERIFY C6: Stream.readTo freed %d delivered buffer(s) (%d ref-counted/pool-backed, %d bytes); counting pool: gets=%d, puts before Free=%d, puts after Free=%d\n",
		b.bufs, b.pooled, b.bytes, verifyC6Pool.gets.Load(), b.puts, verifyC6Pool.puts.Load())
}
