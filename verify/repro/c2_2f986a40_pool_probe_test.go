//go:build ignore

// Audit probe for C2 on branch evalon/grpc-go-tr-2f986a40. Run: go test -v -run '^TestVerifyC2' ./internal/transport -count=1
package transport

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc/mem"
)

type verifyCountingPool struct {
	mem.BufferPool
	gets, puts atomic.Int64
}

func (p *verifyCountingPool) Get(n int) *[]byte { p.gets.Add(1); return p.BufferPool.Get(n) }
func (p *verifyCountingPool) Put(b *[]byte)     { p.puts.Add(1); p.BufferPool.Put(b) }

func TestVerifyC2_CompactionPoolSelection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transportPool := &verifyCountingPool{BufferPool: mem.NewTieredBufferPool(256, 4<<10, 16<<10, 32<<10, 1<<20)}
	framerPool := &verifyCountingPool{BufferPool: mem.NewTieredBufferPool(256, 4<<10, 16<<10, 32<<10, 1<<20)}
	ss := &ServerStream{}
	st := &http2Server{
		activeStreams: map[uint32]*ServerStream{1: ss},
		fc:            &trInFlow{limit: defaultWindowSize},
		controlBuf:    newControlBuffer(ctx.Done()),
		bufferPool:    transportPool,
	}
	stream := &ss.Stream
	stream.id = 1
	stream.fc = inFlow{limit: defaultWindowSize}
	stream.buf.init()
	stream.readRequester = &fakeReadRequester{}
	stream.trReader = transportReader{
		reader:        recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &stream.buf},
		windowHandler: &mockWindowUpdater{f: func(n int) { stream.fc.onRead(uint32(n)) }},
	}
	const frames = 16 * 1024
	var wire bytes.Buffer
	w := http2.NewFramer(&wire, nil)
	for i := 0; i < frames; i++ {
		if err := w.WriteData(1, false, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	fr := newFramer(&wire, 0, 0, false, 0, framerPool)
	for i := 0; i < frames; i++ {
		f, err := fr.readFrame()
		if err != nil {
			t.Fatal(err)
		}
		df := f.(*parsedDataFrame)
		st.handleData(df)
		df.data.Free()
	}
	b := &stream.buf
	b.mu.Lock()
	chunks := 0
	chunkCap := 0
	for _, m := range b.backlog {
		if m.buffer != nil && m.buffer.Len() > 1 {
			chunks++
			chunkCap += cap(m.buffer.ReadOnlyData())
		}
	}
	pendingCap := 0
	if b.pending != nil {
		pendingCap = cap(*b.pending)
	}
	b.mu.Unlock()
	t.Logf("queued %d one-byte DATA frames: compacted chunks in backlog=%d (cap %d bytes) + pending cap=%d", frames, chunks, chunkCap, pendingCap)
	t.Logf("transport-configured pool (http2Server.bufferPool): Get calls=%d Put calls=%d", transportPool.gets.Load(), transportPool.puts.Load())
	t.Logf("framer pool (source of DATA frame buffers): Get calls=%d Put calls=%d", framerPool.gets.Load(), framerPool.puts.Load())
	if chunks > 0 && transportPool.gets.Load() == 0 {
		t.Logf("RESULT: %d compaction chunks were allocated but none came from the transport's configured pool", chunks)
	}
}
