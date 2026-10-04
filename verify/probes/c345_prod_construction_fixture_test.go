//go:build verify_audit

// C3/C4/C5 probe: copy to internal/transport/ on evalon/grpc-go-tr-9253f1d4, then: go test -tags verify_audit -v -run '^TestProd_' google.golang.org/grpc/internal/transport -race -count=1
// This is tests/eval_recv_buffer_compaction_test.go with (a) every `b := &recvBuffer{}; initRecvBufferForTest(b, pool)` replaced by the
// production constructor `&(&http2Client{bufferPool: pool}).newStream(...).buf`, (b) Eval/eval identifiers renamed to Prod/prod, (c) gofmt. Nothing else changed.

package transport

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/mem"
)

const prodFragmentThreshold = 1024

func unusedInitRecvBufferForTest(b *recvBuffer, pool mem.BufferPool) {
	compactionEnabled := !strings.EqualFold(os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), "false")
	if bi, ok := any(b).(interface{ initWithPool(bool, mem.BufferPool) }); ok {
		bi.initWithPool(compactionEnabled, pool)
	} else if bi, ok := any(b).(interface{ initWithPool(mem.BufferPool, bool) }); ok {
		bi.initWithPool(pool, compactionEnabled)
	} else if bi, ok := any(b).(interface{ init(bool, mem.BufferPool) }); ok {
		bi.init(compactionEnabled, pool)
	} else if bi, ok := any(b).(interface{ init(mem.BufferPool, bool) }); ok {
		bi.init(pool, compactionEnabled)
	} else if bi, ok := any(b).(interface{ init(bool) }); ok {
		bi.init(compactionEnabled)
	} else if bi, ok := any(b).(interface{ initWithPool(mem.BufferPool) }); ok {
		if compactionEnabled {
			bi.initWithPool(pool)
		} else {
			if biInit, okInit := any(b).(interface{ init() }); okInit {
				biInit.init()
			} else {
				bi.initWithPool(nil)
			}
		}
	} else if bi, ok := any(b).(interface{ init(mem.BufferPool) }); ok {
		if compactionEnabled {
			bi.init(pool)
		} else {
			bi.init(nil)
		}
	} else if bi, ok := any(b).(interface{ init() }); ok {
		bi.init()
		if compactionEnabled {
			if bi2, ok2 := any(b).(interface{ enableCompaction(mem.BufferPool) }); ok2 {
				bi2.enableCompaction(pool)
			} else if bi2, ok2 := any(b).(interface{ enableCompaction(bool, mem.BufferPool) }); ok2 {
				bi2.enableCompaction(true, pool)
			}
		} else {
			if bi2, ok2 := any(b).(interface{ enableCompaction(bool, mem.BufferPool) }); ok2 {
				bi2.enableCompaction(false, pool)
			}
		}
	} else {
		panic("unsupported recvBuffer initialization signature")
	}
}

func prodDrainRecvBufferForTest(t *testing.T, b *recvBuffer, wantPayload []byte) []byte {
	t.Helper()
	var readPayload []byte
	deadline := time.Now().Add(2 * time.Second)
	for len(readPayload) < len(wantPayload) {
		if time.Now().After(deadline) {
			t.Fatalf("Timeout draining buffer: got %d bytes, want %d bytes", len(readPayload), len(wantPayload))
		}
		select {
		case msg := <-b.c:
			if msg.buffer != nil {
				readPayload = append(readPayload, msg.buffer.ReadOnlyData()...)
				msg.buffer.Free()
			}
			b.load()
		default:
			b.load()
			select {
			case msg := <-b.c:
				if msg.buffer != nil {
					readPayload = append(readPayload, msg.buffer.ReadOnlyData()...)
					msg.buffer.Free()
				}
				b.load()
			default:
				time.Sleep(1 * time.Millisecond)
			}
		}
	}
	return readPayload
}

// prodAssertBacklogForCompactionState checks how many messages a receive buffer
// retains for the requested compaction state, then releases every queued buffer.
func prodAssertBacklogForCompactionState(t *testing.T, label string, b *recvBuffer, compactionEnabled bool, numMessages int) {
	t.Helper()
	if compactionEnabled {
		maxAllowed := numMessages / 16
		if maxAllowed < 1 {
			maxAllowed = 1
		}
		if got := len(b.backlog); got > maxAllowed {
			t.Fatalf("%s: got backlog length %d, want <= %d (compaction enabled)", label, got, maxAllowed)
		}
	} else {
		wantBacklog := numMessages - 1
		if got := len(b.backlog); got != wantBacklog {
			t.Fatalf("%s: got backlog length %d, want %d (compaction disabled)", label, got, wantBacklog)
		}
	}
	select {
	case msg1 := <-b.c:
		if msg1.buffer != nil {
			msg1.buffer.Free()
		}
	default:
		t.Fatalf("%s: expected first message to be in the channel", label)
	}
	for _, msg := range b.backlog {
		if msg.buffer != nil {
			msg.buffer.Free()
		}
	}
}

func TestProd_RecvBufferCompaction(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

	numMessages := prodFragmentThreshold + 2
	payloads := make([][]byte, numMessages)
	for i := range numMessages {
		payloads[i] = []byte{byte(i%251 + 1)}
	}

	for i := range numMessages {
		b.put(recvMsg{buffer: mem.Copy(payloads[i], pool)})
	}

	maxAllowedBacklog := numMessages / 16
	if maxAllowedBacklog < 1 {
		maxAllowedBacklog = 1
	}
	if got := len(b.backlog); got > maxAllowedBacklog {
		t.Fatalf("Got backlog length %d after compaction, want <= %d", got, maxAllowedBacklog)
	}

	wantPayload := make([]byte, 0, numMessages)
	for i := range numMessages {
		wantPayload = append(wantPayload, payloads[i]...)
	}

	readPayload := prodDrainRecvBufferForTest(t, b, wantPayload)
	if !bytes.Equal(readPayload, wantPayload) {
		t.Errorf("Compacted payload mismatch: got %d bytes, want %d bytes", len(readPayload), len(wantPayload))
	}
}

func TestProd_RecvBufferCompactionDisabled(t *testing.T) {
	compactionEnabled := !strings.EqualFold(os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), "false")

	numMessages := prodFragmentThreshold + 2
	payload := []byte{0x0a}
	pool := mem.DefaultBufferPool()

	// Check production stream construction first. A candidate that ignores the
	// environment configuration in production must fail this assertion rather
	// than being masked by a component-level initialization failure.
	t.Run("ProductionStream", func(t *testing.T) {
		client := &http2Client{bufferPool: pool}
		s := client.newStream(context.Background(), &CallHdr{}, nil)
		for range numMessages {
			s.buf.put(recvMsg{buffer: mem.Copy(payload, pool)})
		}
		prodAssertBacklogForCompactionState(t, "ProductionStream", &s.buf, compactionEnabled, numMessages)
	})

	t.Run("ComponentBuffer", func(t *testing.T) {
		b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf
		for range numMessages {
			b.put(recvMsg{buffer: mem.Copy(payload, pool)})
		}
		prodAssertBacklogForCompactionState(t, "ComponentBuffer", b, compactionEnabled, numMessages)
	})
}

func TestProd_RecvBufferCompactionSkippedLargeBuffer(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

	numSmallMessages := prodFragmentThreshold + 1
	payload := []byte{0x0a}

	for range numSmallMessages {
		b.put(recvMsg{buffer: mem.Copy(payload, pool)})
	}

	lenBefore := len(b.backlog)

	largePayload := make([]byte, 1<<20)
	for i := range largePayload {
		largePayload[i] = byte(i%251 + 1)
	}
	origBuf := mem.Copy(largePayload, pool)
	origPtr := &origBuf.ReadOnlyData()[0]
	b.put(recvMsg{buffer: origBuf})

	lenAfter := len(b.backlog)
	if lenAfter == 0 {
		t.Fatalf("Expected non-empty backlog after large buffer, got lenBefore=%d, lenAfter=0", lenBefore)
	}

	lastMsg := b.backlog[lenAfter-1]
	if lastMsg.buffer == nil || lastMsg.buffer.Len() != len(largePayload) {
		t.Fatalf("Expected last backlog entry to hold large payload intact (%d bytes), got %v", len(largePayload), lastMsg.buffer)
	}
	if !bytes.Equal(lastMsg.buffer.ReadOnlyData(), largePayload) {
		t.Fatalf("Full 1 MiB large payload content mismatch: got len=%d, want len=%d", lastMsg.buffer.Len(), len(largePayload))
	}
	if &lastMsg.buffer.ReadOnlyData()[0] != origPtr {
		t.Fatalf("Large buffer backing array was reallocated or recopied")
	}

	select {
	case msg1 := <-b.c:
		msg1.buffer.Free()
	default:
		t.Fatal("Expected first message to be in the channel")
	}
	for _, msg := range b.backlog {
		if msg.buffer != nil {
			msg.buffer.Free()
		}
	}

	// Verify clearly large frames (4 KiB, 16 KiB, 64 KiB) arriving after queued data bypass compaction with zero-copy preservation
	for _, size := range []int{16384, 65536} {
		bLarge := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf
		bLarge.put(recvMsg{buffer: mem.Copy([]byte("occupy"), pool)})
		bLarge.put(recvMsg{buffer: mem.Copy([]byte{0x01}, pool)})

		p := make([]byte, size)
		for i := range p {
			p[i] = byte(i%251 + 1)
		}
		origBuf := mem.Copy(p, pool)
		origPtr := &origBuf.ReadOnlyData()[0]
		bLarge.put(recvMsg{buffer: origBuf})

		wantLen := 2
		if len(bLarge.backlog) != wantLen {
			t.Fatalf("Large frame (%d bytes): expected backlog len=%d, got %d", size, wantLen, len(bLarge.backlog))
		}
		if &bLarge.backlog[wantLen-1].buffer.ReadOnlyData()[0] != origPtr {
			t.Fatalf("Large frame (%d bytes): backing storage was reallocated or recopied", size)
		}
		for _, msg := range bLarge.backlog {
			if msg.buffer != nil {
				msg.buffer.Free()
			}
		}
	}

	t.Run("PoolOwnership_SliceGrowth", func(t *testing.T) {
		inner, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
		if err != nil {
			t.Fatalf("NewBinaryTieredBufferPool failed: %v", err)
		}
		trackPool := &prodTrackingPool{inner: inner, out: make(map[uintptr]int)}
		client := &http2Client{bufferPool: trackPool}
		s := client.newStream(context.Background(), &CallHdr{}, nil)

		var wantPayload []byte

		firstPayload := []byte("occupy")
		wantPayload = append(wantPayload, firstPayload...)
		s.buf.put(recvMsg{buffer: mem.Copy(firstPayload, trackPool)})

		payload8k := make([]byte, 8192)
		for i := range payload8k {
			payload8k[i] = byte(i%251 + 1)
		}
		wantPayload = append(wantPayload, payload8k...)
		s.buf.put(recvMsg{buffer: mem.Copy(payload8k, trackPool)})

		for range 5 {
			tiny := []byte{0x02}
			wantPayload = append(wantPayload, tiny...)
			s.buf.put(recvMsg{buffer: mem.Copy(tiny, trackPool)})
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var gotPayload []byte
		for {
			s.buf.load()
			select {
			case <-ctx.Done():
				t.Fatalf("Timed out waiting to drain buffer: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
			case msg := <-s.buf.c:
				if msg.buffer != nil {
					gotPayload = append(gotPayload, msg.buffer.ReadOnlyData()...)
					msg.buffer.Free()
				}
			default:
				goto drained
			}
		}
	drained:
		if !bytes.Equal(gotPayload, wantPayload) {
			t.Fatalf("Pool ownership payload mismatch: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
		}

		trackPool.mu.Lock()
		outstanding := len(trackPool.out)
		foreign := trackPool.foreign
		trackPool.mu.Unlock()

		if outstanding > 0 {
			t.Fatalf("Tracking pool: %d acquired destination buffers were abandoned and never returned to the pool", outstanding)
		}
		if foreign > 0 {
			t.Fatalf("Tracking pool: %d unacquired foreign buffers were returned to the pool", foreign)
		}
	})

}

func TestProd_RecvBufferErrorResetSafety(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

	numPayloads := prodFragmentThreshold + 50
	payloads := make([][]byte, numPayloads)
	wantPayload := make([]byte, 0, numPayloads)
	for i := range numPayloads {
		payloads[i] = []byte{byte(i%251 + 1)}
		wantPayload = append(wantPayload, payloads[i]...)
	}

	for i := range numPayloads {
		b.put(recvMsg{buffer: mem.Copy(payloads[i], pool)})
	}
	b.put(recvMsg{err: io.EOF})

	var readPayload []byte
	var terminalErr error
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("TestProd_RecvBufferErrorResetSafety: timed out draining receive buffer after terminal error (backlog=%d, chan=%d)", len(b.backlog), len(b.c))
		}
		b.load()
		select {
		case msg := <-b.c:
			if msg.buffer != nil {
				readPayload = append(readPayload, msg.buffer.ReadOnlyData()...)
				msg.buffer.Free()
			}
			if msg.err != nil {
				terminalErr = msg.err
			}
		default:
		}
		if terminalErr != nil || (len(b.backlog) == 0 && len(b.c) == 0) {
			break
		}
	}

	if terminalErr != io.EOF {
		t.Fatalf("Got terminal error %v, want io.EOF", terminalErr)
	}

	if !bytes.Equal(readPayload, wantPayload) {
		t.Errorf("Error reset payload mismatch: got %d bytes, want %d bytes", len(readPayload), len(wantPayload))
	}
}

func TestProd_RecvBufferCompaction_MixedFrames(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

	numMessages := prodFragmentThreshold + 50
	payloads := make([][]byte, numMessages)
	wantPayload := make([]byte, 0, numMessages*4)
	for i := range numMessages {
		size := (i % 7) + 1
		p := make([]byte, size)
		for j := range size {
			p[j] = byte((i*13+j)%251 + 1)
		}
		payloads[i] = p
		wantPayload = append(wantPayload, p...)
	}

	for i := range numMessages {
		b.put(recvMsg{buffer: mem.Copy(payloads[i], pool)})
	}

	maxAllowedBacklog := numMessages / 8
	if maxAllowedBacklog < 1 {
		maxAllowedBacklog = 1
	}
	if got := len(b.backlog); got > maxAllowedBacklog {
		t.Fatalf("Got backlog length %d after mixed frame compaction, want <= %d", got, maxAllowedBacklog)
	}

	readPayload := prodDrainRecvBufferForTest(t, b, wantPayload)
	if !bytes.Equal(readPayload, wantPayload) {
		t.Errorf("Mixed frame payload mismatch: got %d bytes, want %d bytes", len(readPayload), len(wantPayload))
	}

	// Also verify that alternating small (1-byte) and medium (2-KiB) frames
	// keep retained backing capacity bounded without geometric expansion.
	bAlternating := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

	var wantAltPayload []byte
	putAlt := func(p []byte) {
		wantAltPayload = append(wantAltPayload, p...)
		bAlternating.put(recvMsg{buffer: mem.Copy(p, pool)})
	}

	putAlt([]byte{0x01})
	putAlt([]byte{0x02})

	for i := range 24 {
		putAlt([]byte{byte(i + 10)})
		p2k := make([]byte, 2048)
		for j := range p2k {
			p2k[j] = byte((i*17 + j) % 251)
		}
		putAlt(p2k)
	}

	var totalRetained int
	for _, m := range bAlternating.backlog {
		if m.buffer != nil {
			totalRetained += cap(m.buffer.ReadOnlyData())
		}
	}
	maxAllowedCap := 4*len(wantAltPayload) + 64*1024
	if totalRetained > maxAllowedCap {
		t.Fatalf("Alternating frames: retained capacity %d exceeds bound %d (payload=%d)", totalRetained, maxAllowedCap, len(wantAltPayload))
	}

	readAltPayload := prodDrainRecvBufferForTest(t, bAlternating, wantAltPayload)
	if !bytes.Equal(readAltPayload, wantAltPayload) {
		t.Fatalf("Alternating frames payload mismatch: got %d bytes, want %d bytes", len(readAltPayload), len(wantAltPayload))
	}
}

func TestProd_RecvBufferCompaction_MultiCycleMemoryBound(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

	t.Run("MultiCycleBursts", func(t *testing.T) {
		const numCycles = 3
		const framesPerCycle = prodFragmentThreshold + 100
		var wantPayload []byte
		var readPayload []byte

		for cycle := range numCycles {
			cyclePayloads := make([][]byte, framesPerCycle)
			for i := range framesPerCycle {
				size := (i % 5) + 1
				p := make([]byte, size)
				for j := range size {
					p[j] = byte((cycle*31+i*7+j)%251 + 1)
				}
				cyclePayloads[i] = p
				wantPayload = append(wantPayload, p...)
			}

			for i := range framesPerCycle {
				b.put(recvMsg{buffer: mem.Copy(cyclePayloads[i], pool)})
			}

			maxAllowedBacklog := prodFragmentThreshold / 2
			if maxAllowedBacklog < 1 {
				maxAllowedBacklog = 1
			}
			if got := len(b.backlog); got > maxAllowedBacklog {
				t.Fatalf("Cycle %d: backlog length %d exceeded bound %d", cycle, got, maxAllowedBacklog)
			}

			// Partially drain approximately one frame per cycle
			b.load()
			select {
			case msg := <-b.c:
				if msg.buffer != nil {
					readPayload = append(readPayload, msg.buffer.ReadOnlyData()...)
					msg.buffer.Free()
				}
			default:
			}
		}

		// Drain all remaining data from the buffer across all cycles
		remainingWant := wantPayload[len(readPayload):]
		remainingRead := prodDrainRecvBufferForTest(t, b, remainingWant)
		readPayload = append(readPayload, remainingRead...)

		if !bytes.Equal(readPayload, wantPayload) {
			t.Fatalf("Multi-cycle payload mismatch: got %d bytes, want %d bytes", len(readPayload), len(wantPayload))
		}
	})

	t.Run("IncrementalMessageAssembly", func(t *testing.T) {
		bAssembly := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		reader := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: bAssembly}
		var retained mem.BufferSlice
		defer retained.Free()

		const cycles = 1024
		var totalPayload int
		var wantPayload []byte
		var gotPayload []byte

		for i := range cycles {
			for j := range 3 {
				p := []byte{byte((i*7+j)%251 + 1)}
				bAssembly.put(recvMsg{buffer: mem.Copy(p, pool)})
				wantPayload = append(wantPayload, p...)
				totalPayload++
			}
			for remaining := 3; remaining > 0; {
				buf, err := reader.Read(remaining)
				if err != nil {
					t.Fatalf("cycle %d: read error: %v", i, err)
				}
				remaining -= buf.Len()
				gotPayload = append(gotPayload, buf.ReadOnlyData()...)
				retained = append(retained, buf)
			}
		}

		if !bytes.Equal(gotPayload, wantPayload) {
			t.Fatalf("Incremental assembly payload mismatch: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
		}

		var distinctCap int
		seen := make(map[uintptr]bool)
		for _, buf := range retained {
			data := buf.ReadOnlyData()
			if len(data) > 0 {
				ptr := uintptr(unsafe.Pointer(&data[0]))
				if !seen[ptr] {
					seen[ptr] = true
					distinctCap += cap(data)
				}
			}
		}
		if limit := 4*totalPayload + 64*1024; distinctCap > limit {
			t.Fatalf("Distinct retained capacity %d exceeded limit %d (payload: %d bytes)", distinctCap, limit, totalPayload)
		}
	})
}

func TestProd_RecvBufferConfiguredPoolAcquisition(t *testing.T) {
	t.Run("UnpooledConsolidation", func(t *testing.T) {
		inner, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
		if err != nil {
			t.Fatalf("NewBinaryTieredBufferPool failed: %v", err)
		}
		trackPool := &prodTrackingPool{inner: inner, out: make(map[uintptr]int)}
		client := &http2Client{bufferPool: trackPool}
		s := client.newStream(context.Background(), &CallHdr{}, nil)

		numMessages := prodFragmentThreshold + 2
		var wantPayload []byte
		for i := range numMessages {
			p := []byte{byte((i*7)%251 + 1)}
			wantPayload = append(wantPayload, p...)
			s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var gotPayload []byte
		for {
			s.buf.load()
			select {
			case <-ctx.Done():
				t.Fatalf("Timed out waiting to drain buffer: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
			case msg := <-s.buf.c:
				if msg.buffer != nil {
					gotPayload = append(gotPayload, msg.buffer.ReadOnlyData()...)
					msg.buffer.Free()
				}
				if len(gotPayload) == len(wantPayload) {
					goto drained1
				}
			default:
				time.Sleep(time.Millisecond)
			}
		}
	drained1:
		if !bytes.Equal(gotPayload, wantPayload) {
			t.Fatalf("Payload mismatch: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
		}

		trackPool.mu.Lock()
		acquisitions := trackPool.acquisitions
		outstanding := len(trackPool.out)
		foreign := trackPool.foreign
		trackPool.mu.Unlock()

		if acquisitions == 0 {
			t.Fatalf("Compaction destination buffer was never acquired from the configured buffer pool")
		}
		if outstanding > 0 {
			t.Fatalf("Tracking pool: %d acquired destination buffers were abandoned and never returned", outstanding)
		}
		if foreign > 0 {
			t.Fatalf("Tracking pool: %d unacquired foreign buffers were returned to the pool", foreign)
		}
	})

	t.Run("SliceGrowthPoolOwnership", func(t *testing.T) {
		inner, err := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
		if err != nil {
			t.Fatalf("NewBinaryTieredBufferPool failed: %v", err)
		}
		trackPool := &prodTrackingPool{inner: inner, out: make(map[uintptr]int)}
		client := &http2Client{bufferPool: trackPool}
		s := client.newStream(context.Background(), &CallHdr{}, nil)

		var wantPayload []byte
		first := mem.Copy([]byte("occupy"), trackPool)
		wantPayload = append(wantPayload, []byte("occupy")...)
		s.buf.put(recvMsg{buffer: first})

		payload8k := make([]byte, 8192)
		for i := range payload8k {
			payload8k[i] = byte(i%251 + 1)
		}
		wantPayload = append(wantPayload, payload8k...)
		s.buf.put(recvMsg{buffer: mem.Copy(payload8k, trackPool)})

		for range 5 {
			tiny := []byte{0x02}
			wantPayload = append(wantPayload, tiny...)
			s.buf.put(recvMsg{buffer: mem.Copy(tiny, trackPool)})
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var gotPayload []byte
		for {
			s.buf.load()
			select {
			case <-ctx.Done():
				t.Fatalf("Timed out waiting to drain buffer: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
			case msg := <-s.buf.c:
				if msg.buffer != nil {
					gotPayload = append(gotPayload, msg.buffer.ReadOnlyData()...)
					msg.buffer.Free()
				}
				if len(gotPayload) == len(wantPayload) {
					goto drained2
				}
			default:
				time.Sleep(time.Millisecond)
			}
		}
	drained2:
		if !bytes.Equal(gotPayload, wantPayload) {
			t.Fatalf("Payload mismatch: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
		}

		trackPool.mu.Lock()
		outstanding := len(trackPool.out)
		foreign := trackPool.foreign
		trackPool.mu.Unlock()

		if outstanding > 0 {
			t.Fatalf("Tracking pool: %d acquired destination buffers were abandoned during slice growth", outstanding)
		}
		if foreign > 0 {
			t.Fatalf("Tracking pool: %d unacquired foreign buffers were returned during slice growth", foreign)
		}
	})

	t.Run("ExactCapacitySmallDestination", func(t *testing.T) {
		exactPool := &prodExactCapacityPool{out: make(map[uintptr]int)}
		client := &http2Client{bufferPool: exactPool}
		s := client.newStream(context.Background(), &CallHdr{}, nil)

		var wantPayload []byte
		for i := range 10 {
			p := make([]byte, 100)
			for j := range p {
				p[j] = byte((i*11+j)%251 + 1)
			}
			wantPayload = append(wantPayload, p...)
			s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var gotPayload []byte
		for {
			s.buf.load()
			select {
			case <-ctx.Done():
				t.Fatalf("Timed out waiting to drain buffer: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
			case msg := <-s.buf.c:
				if msg.buffer != nil {
					gotPayload = append(gotPayload, msg.buffer.ReadOnlyData()...)
					msg.buffer.Free()
				}
				if len(gotPayload) == len(wantPayload) {
					goto drained3
				}
			default:
				time.Sleep(time.Millisecond)
			}
		}
	drained3:
		if !bytes.Equal(gotPayload, wantPayload) {
			t.Fatalf("Payload mismatch: got %d bytes, want %d bytes", len(gotPayload), len(wantPayload))
		}

		exactPool.mu.Lock()
		acquisitions := append([]int(nil), exactPool.acquisitions...)
		outstanding := len(exactPool.out)
		foreign := exactPool.foreign
		exactPool.mu.Unlock()

		var smallAcquisitions int
		for _, capSize := range acquisitions {
			if capSize <= 1024 {
				smallAcquisitions++
			}
		}
		if smallAcquisitions > 0 && outstanding > 0 {
			t.Fatalf("Exact capacity pool: %d acquired destination buffers <= 1024 bytes were abandoned and never returned to the pool (mem.NewBuffer SliceBuffer leak)", outstanding)
		}
		if foreign > 0 {
			t.Fatalf("Exact capacity pool: %d unacquired foreign buffers were returned", foreign)
		}
	})
}

type prodTrackingPool struct {
	inner        mem.BufferPool
	mu           sync.Mutex
	out          map[uintptr]int
	acquisitions int
	foreign      int
}

func (p *prodTrackingPool) Get(n int) *[]byte {
	b := p.inner.Get(n)
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	p.out[ptr] = cap(*b)
	p.acquisitions++
	p.mu.Unlock()
	return b
}

func (p *prodTrackingPool) Put(b *[]byte) {
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	expectedCap, ok := p.out[ptr]
	if ok {
		delete(p.out, ptr)
		p.mu.Unlock()
		if cap(*b) >= expectedCap {
			p.inner.Put(b)
		}
	} else {
		p.foreign++
		p.mu.Unlock()
	}
}

type prodExactCapacityPool struct {
	mu           sync.Mutex
	out          map[uintptr]int
	acquisitions []int
	returned     []int
	foreign      int
}

func (p *prodExactCapacityPool) Get(n int) *[]byte {
	b := make([]byte, 0, n)
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(b)))
	p.mu.Lock()
	p.out[ptr] = n
	p.acquisitions = append(p.acquisitions, n)
	p.mu.Unlock()
	return &b
}

func (p *prodExactCapacityPool) Put(b *[]byte) {
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	if capa, ok := p.out[ptr]; ok {
		delete(p.out, ptr)
		p.returned = append(p.returned, capa)
	} else {
		p.foreign++
	}
	p.mu.Unlock()
}
