// Run: git worktree add --detach /tmp/wt-64726da8 <claims-remote>/evalon/grpc-go-tr-64726da8 && cp verify/repro/c4_homogeneous_copy_test.go /tmp/wt-64726da8/internal/transport/verify_c4_homogeneous_copy_test.go && cd /tmp/wt-64726da8 && go test -tags verify_repro -v -run '^TestVerify_C4_' ./internal/transport -count=1
// (add GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false for the control run)

//go:build verify_repro

package transport

import (
	"bytes"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
)

// verifyC4Pool hands out exact-size storage and records every Get and Put.
type verifyC4Pool struct {
	mu   sync.Mutex
	out  map[uintptr]int
	gets int
	puts int
}

func (p *verifyC4Pool) Get(n int) *[]byte {
	b := make([]byte, n)
	p.mu.Lock()
	p.gets++
	p.out[uintptr(unsafe.Pointer(unsafe.SliceData(b)))] = n
	p.mu.Unlock()
	return &b
}

func (p *verifyC4Pool) Put(b *[]byte) {
	p.mu.Lock()
	p.puts++
	delete(p.out, uintptr(unsafe.Pointer(unsafe.SliceData(*b))))
	p.mu.Unlock()
}

func verifyC4Run(t *testing.T, payloadSize, n int) {
	pool := &verifyC4Pool{out: map[uintptr]int{}}
	var b recvBuffer
	b.init()

	// Occupy the channel so that everything that follows goes to the backlog.
	b.put(recvMsg{buffer: mem.SliceBuffer([]byte{0xff})})

	// Pre-build n homogeneous pooled payloads (as the framer would for DATA
	// frames above the pooling threshold) and remember their storage.
	inputs := make([]mem.Buffer, n)
	inputPtr := map[uintptr]int{}
	var want []byte
	for i := range n {
		h := pool.Get(payloadSize)
		for j := range *h {
			(*h)[j] = byte(i*31 + j%251 + 1)
		}
		want = append(want, *h...)
		inputPtr[uintptr(unsafe.Pointer(unsafe.SliceData(*h)))] = i
		inputs[i] = mem.NewBuffer(h, pool)
	}
	getsBefore := pool.gets

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for _, in := range inputs {
		b.put(recvMsg{buffer: in})
	}
	runtime.ReadMemStats(&m1)

	b.mu.Lock()
	preserved, relocated := 0, 0
	var got []byte
	var lens []int
	for _, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		got = append(got, d...)
		lens = append(lens, len(d))
		if _, ok := inputPtr[uintptr(unsafe.Pointer(unsafe.SliceData(d)))]; ok {
			preserved++
		} else {
			relocated++
		}
	}
	backlogLen := len(b.backlog)
	b.mu.Unlock()

	pool.mu.Lock()
	inputsFreedAtPut := pool.puts
	stillOwned := len(pool.out)
	pool.mu.Unlock()

	t.Logf("compaction enabled=%v payload=%d bytes x %d (adjacent pair total %d, http2MaxFrameLen %d)", envconfig.EnableReceiveBufferCompaction, payloadSize, n, 2*payloadSize, http2MaxFrameLen)
	t.Logf("  backlog entries=%d lens=%v", backlogLen, lens)
	t.Logf("  backing storage: entries on an original input allocation=%d, entries on storage that is none of the inputs=%d", preserved, relocated)
	t.Logf("  input pooled buffers released (pool.Put) during put()=%d of %d, still referenced=%d, pool.Get during put()=%d", inputsFreedAtPut, n, stillOwned, pool.gets-getsBefore)
	t.Logf("  heap during put(): mallocs=%d bytes=%d (payload bytes queued=%d)", m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc, n*payloadSize)
	t.Logf("  payload bytes intact and in order=%v", bytes.Equal(got, want))
	t.Logf("RESULT C4 payload=%d: copied_into_new_storage=%v (relocated entries=%d, inputs freed at put=%d, bytes allocated=%d)", payloadSize, relocated > 0 && inputsFreedAtPut > 0, relocated, inputsFreedAtPut, m1.TotalAlloc-m0.TotalAlloc)
}

func TestVerify_C4_Homogeneous4KiB(t *testing.T)  { verifyC4Run(t, 4096, 8) }
func TestVerify_C4_Homogeneous8KiB(t *testing.T)  { verifyC4Run(t, 8192, 8) }
func TestVerify_C4_Control16KiB(t *testing.T)     { verifyC4Run(t, 16384, 8) }
func TestVerify_C4_Control8KiBPlus1(t *testing.T) { verifyC4Run(t, 8193, 8) }
