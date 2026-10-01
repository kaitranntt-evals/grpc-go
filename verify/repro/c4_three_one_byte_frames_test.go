// Run: sh verify/repro/c4_three_one_byte_frames.sh (copies this file into internal/transport on evalon/grpc-go-tr-ad9c633e and runs `go test -tags verify_repro -run 'TestVerifyC4' -v ./internal/transport`).

//go:build verify_repro

package transport

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/net/http2"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
)

// c4Retained inspects the channel entry and the backlog without consuming
// anything and sums the capacity of every distinct backing allocation once.
func c4Retained(t *testing.T, b *recvBuffer) (payload, capacity int) {
	t.Helper()
	seen := map[*byte]bool{}
	add := func(where string, buf mem.Buffer) {
		d := buf.ReadOnlyData()
		payload += len(d)
		base := unsafe.SliceData(d[:cap(d)])
		dup := seen[base]
		if !dup {
			seen[base] = true
			capacity += cap(d)
		}
		t.Logf("  %-10s payload=%d cap=%d backing=%p alreadyCounted=%v", where, len(d), cap(d), base, dup)
	}
	select {
	case m := <-b.c:
		add("chan", m.buffer)
		b.c <- m
	default:
		t.Logf("  chan       (empty)")
	}
	for i, m := range b.backlog {
		add(fmt.Sprintf("backlog[%d]", i), m.buffer)
	}
	return
}

// Component level: three one-byte frames put into a fresh recvBuffer, nothing
// consumed.
func TestVerifyC4_ThreeOneByteFrames_RecvBuffer(t *testing.T) {
	for _, tc := range []struct {
		frames  int
		enabled bool
	}{{3, true}, {3, false}, {2, true}} {
		t.Run(fmt.Sprintf("frames=%d/compaction=%v", tc.frames, tc.enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, tc.enabled)
			var b recvBuffer
			b.init()
			for i := 0; i < tc.frames; i++ {
				b.put(recvMsg{buffer: mem.Copy([]byte{byte(i + 1)}, mem.DefaultBufferPool())})
			}
			payload, capacity := c4Retained(t, &b)
			t.Logf("RESULT frames=%d compaction=%v: pendingPayload=%d bytes, retained payload-backing capacity=%d bytes (1 KiB = 1024)", tc.frames, tc.enabled, payload, capacity)
			if capacity > 1024 {
				t.Errorf("C4 CONFIRMED: %d one-byte frames retain %d bytes of backing capacity (> 1024)", tc.frames, capacity)
			}
		})
	}
}

// Production path: three one-byte DATA frames parsed by the real framer and
// delivered through http2Server.handleData to a stream nobody reads from.
func TestVerifyC4_ThreeOneByteFrames_ServerHandleData(t *testing.T) {
	var wire bytes.Buffer
	w := http2.NewFramer(&wire, nil)
	for i := 0; i < 3; i++ {
		if err := w.WriteData(1, false, []byte{byte(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction=%v", enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
			ss := &ServerStream{}
			tr := &http2Server{
				activeStreams: map[uint32]*ServerStream{1: ss},
				fc:            &trInFlow{limit: initialWindowSize},
				controlBuf:    newControlBuffer(nil),
			}
			stream := &ss.Stream
			stream.id = 1
			stream.fc.limit = initialWindowSize
			stream.buf.init()
			fr := newFramer(bytes.NewBuffer(wire.Bytes()), 0, 0, false, 0, mem.DefaultBufferPool())
			for i := 0; i < 3; i++ {
				frame, err := fr.readFrame()
				if err != nil {
					t.Fatal(err)
				}
				data := frame.(*parsedDataFrame)
				tr.handleData(data)
				data.data.Free()
			}
			payload, capacity := c4Retained(t, &stream.buf)
			t.Logf("RESULT handleData compaction=%v: pendingPayload=%d bytes, retained payload-backing capacity=%d bytes", enabled, payload, capacity)
			if capacity > 1024 {
				t.Errorf("C4 CONFIRMED on the production path: 3 one-byte DATA frames retain %d bytes of backing capacity (> 1024)", capacity)
			}
		})
	}
}

// Live-heap cross-check: many independent buffers, each holding three unread
// one-byte frames. Uses a signed delta of two GC-settled samples.
func TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer(t *testing.T) {
	const buffers = 10000
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction=%v", enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
			heap := func() int64 {
				runtime.GC()
				runtime.GC()
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				return int64(ms.HeapAlloc)
			}
			bufs := make([]recvBuffer, buffers)
			for i := range bufs {
				bufs[i].init()
			}
			before := heap()
			for i := range bufs {
				for j := 0; j < 3; j++ {
					bufs[i].put(recvMsg{buffer: mem.Copy([]byte{byte(j + 1)}, mem.DefaultBufferPool())})
				}
			}
			after := heap()
			runtime.KeepAlive(bufs)
			t.Logf("RESULT compaction=%v: %d buffers x 3 unread one-byte frames (%d payload bytes) retain %d live heap bytes = %d bytes per buffer", enabled, buffers, 3*buffers, after-before, (after-before)/buffers)
		})
	}
}
