// Run: sh verify/repro/c3_heap_delta.sh (copies this file into internal/transport on an unmodified evalon/grpc-go-tr-8401755a checkout and runs `go test -tags verify_repro -run 'TestVerifyC3Natural' -v ./internal/transport`).

//go:build verify_repro

package transport

import (
	"runtime"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/mem"
)

// c3Ballast is unrelated process memory: live when the branch's test takes its
// first heap sample, released while the test is queuing frames.
var c3Ballast []byte

// c3ReleasingPool is a pass-through buffer pool that drops c3Ballast the first
// time the receive buffer asks for a compaction buffer, i.e. strictly between
// the test's two heapAllocBytes() samples. It changes nothing about how the
// receive buffer behaves.
type c3ReleasingPool struct {
	mem.BufferPool
	mu       sync.Mutex
	released bool
}

func (p *c3ReleasingPool) Get(n int) *[]byte {
	p.mu.Lock()
	if !p.released && c3CalledFromCompaction() {
		p.released = true
		c3Ballast = nil
	}
	p.mu.Unlock()
	return p.BufferPool.Get(n)
}

func c3CalledFromCompaction() bool {
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		f, more := frames.Next()
		if strings.HasSuffix(f.Function, ".compactLocked") {
			return true
		}
		if !more {
			return false
		}
	}
}

// TestVerifyC3Natural runs the branch's own memory-regression tests, with no
// change to them or to heapAllocBytes(), in a process where 8 MiB of unrelated
// heap is freed between the two samples.
func TestVerifyC3Natural(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"TestRecvBuffer_CompactsTinyPayloads", s{}.TestRecvBuffer_CompactsTinyPayloads},
		{"TestServerReceiveBufferCompaction_ManyTinyDataFrames", s{}.TestServerReceiveBufferCompaction_ManyTinyDataFrames},
	} {
		orig := mem.DefaultBufferPool()
		setDefaultPool := internal.SetDefaultBufferPool.(func(mem.BufferPool))
		for _, ballast := range []int{0, 8 << 20} {
			pool := &c3ReleasingPool{BufferPool: orig}
			if ballast > 0 {
				c3Ballast = make([]byte, ballast)
				for i := range c3Ballast {
					c3Ballast[i] = 1
				}
				setDefaultPool(pool)
			}
			name := tt.name + "/unrelated_heap_freed_between_samples="
			if ballast > 0 {
				name += "8MiB"
			} else {
				name += "none"
			}
			passed := t.Run(name, tt.run)
			setDefaultPool(orig)
			t.Logf("OBSERVED %s: ballast released inside measurement window=%v, branch test passed=%v", name, pool.released, passed)
			c3Ballast = nil
		}
	}
}
