// Run: cp verify/repro/c4_c6_handler_reads_test.go internal/transport/zz_c4_c6_handler_reads_test.go && go test -tags verify_probe -v -run 'TestVerify_HandlerReads' ./internal/transport -count=1 ; rm internal/transport/zz_c4_c6_handler_reads_test.go

//go:build verify_probe

package transport

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"
)

// verifyTrackingPool wraps a real pool and records every outstanding buffer.
type verifyTrackingPool struct {
	mem.BufferPool
	mu          sync.Mutex
	outstanding map[*[]byte]int // ptr -> cap at Get
	gets        map[int]int     // requested length -> count
}

func (p *verifyTrackingPool) Get(n int) *[]byte {
	b := p.BufferPool.Get(n)
	p.mu.Lock()
	p.outstanding[b] = cap(*b)
	p.gets[n]++
	p.mu.Unlock()
	return b
}

func (p *verifyTrackingPool) Put(b *[]byte) {
	p.mu.Lock()
	delete(p.outstanding, b)
	p.mu.Unlock()
	p.BufferPool.Put(b)
}

func (p *verifyTrackingPool) snapshot() (n, capSum int, distinctArrays int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	arrays := map[*byte]struct{}{}
	for b, c := range p.outstanding {
		capSum += c
		arrays[unsafe.SliceData((*b)[:1])] = struct{}{}
	}
	return len(p.outstanding), capSum, len(arrays)
}

func verifyHandlerReads(t *testing.T, reads, readSize int) {
	t.Logf("GOARCH=%s uintptr=%d bytes recvMsgSize=%d utilizationFactor=%d compactionThreshold=%d EnableReceiveBufferCompaction=%v",
		runtime.GOARCH, unsafe.Sizeof(uintptr(0)), recvMsgSize, utilizationFactor, compactionThreshold, envconfig.EnableReceiveBufferCompaction)
	t.Logf("utilization comparison for one read: recvMsgSize+payload = %d ; utilizationFactor*payload = %d ; reset(no tracking) = %v",
		recvMsgSize+readSize, utilizationFactor*readSize, recvMsgSize+readSize <= utilizationFactor*readSize)

	pool := &verifyTrackingPool{BufferPool: mem.DefaultBufferPool(), outstanding: map[*[]byte]int{}, gets: map[int]int{}}
	st := newHandleStreamTest(t, nil)
	st.ht.bufferPool = pool // the pool the production handler transport reads with

	streamCh := make(chan *ServerStream, 1)
	release := make(chan struct{})
	handleStream := func(s *ServerStream) {
		streamCh <- s
		<-release // slow application: does not read
		st.bodyw.Close()
		s.WriteStatus(status.New(codes.OK, ""))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		st.ht.HandleStreams(ctx, func(s *ServerStream) { go handleStream(s) })
	}()
	var s *ServerStream
	select {
	case s = <-streamCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for stream")
	}

	payload := make([]byte, readSize)
	for i := 0; i < reads; i++ {
		// io.Pipe is synchronous: one Write of readSize bytes is consumed by
		// exactly one req.Body.Read in HandleStreams' reader goroutine.
		if _, err := st.bodyw.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	// Wait until every read has been queued.
	var queued, backlogLen, payloadBytes, capBytes, suffixLen, suffixBytes int
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.buf.mu.Lock()
		backlogLen = len(s.buf.backlog)
		queued = backlogLen + len(s.buf.c)
		payloadBytes, capBytes = 0, 0
		for _, m := range s.buf.backlog {
			payloadBytes += m.buffer.Len()
			capBytes += cap(m.buffer.ReadOnlyData())
		}
		suffixLen, suffixBytes = s.buf.uncompactedSuffixLen, s.buf.uncompactedBytes
		s.buf.mu.Unlock()
		if payloadBytes+readSize*len(s.buf.c) >= reads*readSize || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	n, capSum, arrays := pool.snapshot()
	pool.mu.Lock()
	gets := map[int]int{}
	for k, v := range pool.gets {
		gets[k] = v
	}
	pool.mu.Unlock()

	t.Logf("reads=%d readSize=%d total payload=%d bytes (%s KiB)", reads, readSize, reads*readSize, strconv.Itoa(reads*readSize/1024))
	t.Logf("queued recvMsgs=%d (backlog=%d + chan=%d); backlog payload=%d bytes; backlog backing cap=%d bytes", queued, backlogLen, queued-backlogLen, payloadBytes, capBytes)
	t.Logf("tracking after reads: uncompactedSuffixLen=%d uncompactedBytes=%d", suffixLen, suffixBytes)
	t.Logf("pool.Get calls by requested size: %v", gets)
	t.Logf("RETAINED pool buffers (Get without Put)=%d, distinct backing arrays=%d, total backing cap=%d bytes (%.2f MiB) [includes 1 buffer held by the blocked reader goroutine]",
		n, arrays, capSum, float64(capSum)/(1<<20))

	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for HandleStreams")
	}
}

func TestVerify_HandlerReads_C4_512x64(t *testing.T)       { verifyHandlerReads(t, 512, 64) }
func TestVerify_HandlerReads_C6_1024x64(t *testing.T)      { verifyHandlerReads(t, 1024, 64) }
func TestVerify_HandlerReads_Control_1024x48(t *testing.T) { verifyHandlerReads(t, 1024, 48) }
