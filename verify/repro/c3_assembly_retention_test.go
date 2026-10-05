// Run (on branch evalon/grpc-go-tr-247ab7e8): cp c3_assembly_retention_test.go internal/transport/zz_verify_c3_test.go && go test -v -count=1 -run '^TestVerifyC3' ./internal/transport ; rm internal/transport/zz_verify_c3_test.go

package transport

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

// verifyC3Distinct mirrors the eval fixture's evalCountDistinctBackingCapacity:
// buffers are grouped by the end address of their backing array and the
// largest capacity seen per group is summed.
func verifyC3Distinct(bs mem.BufferSlice) (distinctCap, allocations int, payload []byte) {
	seen := map[uintptr]int{}
	for _, buf := range bs {
		d := buf.ReadOnlyData()
		if len(d) == 0 {
			continue
		}
		payload = append(payload, d...)
		end := uintptr(unsafe.Pointer(&d[0])) + uintptr(cap(d))
		if cap(d) > seen[end] {
			seen[end] = cap(d)
		}
	}
	for _, c := range seen {
		distinctCap += c
	}
	return distinctCap, len(seen), payload
}

// verifyC3Queued returns every buffer currently held by b (delivery channel,
// backlog and the in-progress compaction buffer) without consuming them.
func verifyC3Queued(b *recvBuffer) (bs mem.BufferSlice, bytesQueued int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case m := <-b.c:
		if m.buffer != nil {
			bs = append(bs, m.buffer)
		}
		b.c <- m
	default:
	}
	for _, m := range b.backlog {
		if m.buffer != nil {
			bs = append(bs, m.buffer)
		}
	}
	if b.compactBuf != nil {
		bs = append(bs, mem.SliceBuffer(*b.compactBuf))
	}
	for _, x := range bs {
		bytesQueued += x.Len()
	}
	return bs, bytesQueued
}

type verifyC3Workload struct {
	name      string
	cycles    int
	frameSize int // each cycle is 3 frames of this size, i.e. a 3*frameSize message
}

var verifyC3Workloads = []verifyC3Workload{
	{"4096x3B", 4096, 1},    // 4,096 three-byte cycles; bound 4*12288+65536 = 114,688
	{"256x1800B", 256, 600}, // 256 1,800-byte cycles (3 x 600-byte frames); bound 4*460800+65536 = 1,908,736
}

func verifyC3Pattern(i, j, n int) []byte {
	p := make([]byte, n)
	for k := range p {
		p[k] = byte((i*13+j*7+k)%251 + 1)
	}
	return p
}

// TestVerifyC3_DirectPut replays the eval fixture's IncrementalMessageAssembly
// loop (put 3 frames, read the 3-frame message, retain what was read) on a
// production client stream's recvBuffer with the claim's two workloads.
func TestVerifyC3_DirectPut(t *testing.T) {
	def := mem.DefaultBufferPool()
	tiered, err := mem.NewBinaryTieredBufferPool(12, 14, 15, 20) // the fixture's tiered pool
	if err != nil {
		t.Fatal(err)
	}
	type input struct {
		name string
		mk   func(p []byte, pool mem.BufferPool) mem.Buffer
	}
	inputs := []input{
		{"mem.Copy(fixture)", func(p []byte, pool mem.BufferPool) mem.Buffer { return mem.Copy(p, pool) }},
		{"framer(readDataFrame: make+SliceBuffer below 1KiB)", func(p []byte, pool mem.BufferPool) mem.Buffer {
			// mirrors http_util.go readDataFrame for payloads <= pooling threshold
			if mem.IsBelowBufferPoolingThreshold(len(p)) {
				buf := make([]byte, len(p))
				copy(buf, p)
				return mem.SliceBuffer(buf)
			}
			h := pool.Get(len(p))
			copy(*h, p)
			return mem.NewBuffer(h, pool)
		}},
	}
	pools := []struct {
		name string
		pool mem.BufferPool
	}{{"default", def}, {"tiered(12,14,15,20)", tiered}}
	for _, pl := range pools {
		h := pl.pool.Get(recvBufferCompactionSize)
		t.Logf("pool %s: Get(recvBufferCompactionSize=%d) -> cap %d", pl.name, recvBufferCompactionSize, cap(*h))
		pl.pool.Put(h)
	}

	for _, w := range verifyC3Workloads {
		for _, pl := range pools {
			for _, in := range inputs {
				for _, mode := range []string{"read-each-cycle-and-retain", "never-read(left queued in recvBuffer)"} {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", w.name, pl.name, in.name, mode), func(t *testing.T) {
						client := &http2Client{bufferPool: pl.pool}
						s := client.newStream(context.Background(), &CallHdr{}, nil)
						b := &s.buf
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						defer cancel()
						reader := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
						var retained mem.BufferSlice
						var want []byte
						for i := 0; i < w.cycles; i++ {
							for j := 0; j < 3; j++ {
								p := verifyC3Pattern(i, j, w.frameSize)
								want = append(want, p...)
								b.put(recvMsg{buffer: in.mk(p, pl.pool)})
							}
							if mode != "read-each-cycle-and-retain" {
								continue
							}
							for remaining := 3 * w.frameSize; remaining > 0; {
								buf, err := reader.Read(remaining)
								if err != nil {
									t.Fatalf("cycle %d: %v", i, err)
								}
								remaining -= buf.Len()
								retained = append(retained, buf)
							}
						}
						if mode != "read-each-cycle-and-retain" {
							retained, _ = verifyC3Queued(b)
						}
						distinct, allocs, got := verifyC3Distinct(retained)
						if !bytes.Equal(got, want) {
							t.Fatalf("payload mismatch: got %d bytes want %d", len(got), len(want))
						}
						limit := 4*len(want) + 64*1024
						verdict := "WITHIN"
						if distinct > limit {
							verdict = "EXCEEDS"
						}
						t.Logf("RESULT %s pool=%s input=%s mode=%s: payload=%d B, distinct backing allocations=%d, retained capacity=%d B, bound=%d B -> %s", w.name, pl.name, in.name, mode, len(want), allocs, distinct, limit, verdict)
						if distinct > limit {
							t.Errorf("retained capacity %d exceeds bound %d", distinct, limit)
						}
					})
				}
			}
		}
	}
}

// verifyC3RawServer is a raw HTTP/2 peer: it answers the first stream with
// response headers and then, for every value received on cycles, writes the
// given DATA frames on that stream.
func verifyC3RawServer(lis net.Listener, cycles <-chan [][]byte, errCh chan<- error) {
	conn, err := lis.Accept()
	if err != nil {
		errCh <- err
		return
	}
	defer conn.Close()
	if _, err := io.ReadFull(conn, make([]byte, len(clientPreface))); err != nil {
		errCh <- err
		return
	}
	bw := bufio.NewWriter(conn)
	fr := http2.NewFramer(bw, conn)
	fr.WriteSettings()
	bw.Flush()
	var streamID uint32
	for streamID == 0 {
		f, err := fr.ReadFrame()
		if err != nil {
			errCh <- err
			return
		}
		if h, ok := f.(*http2.HeadersFrame); ok {
			streamID = h.StreamID
		}
	}
	go func() { // discard WINDOW_UPDATE / PING / SETTINGS from the client
		for {
			if _, err := fr.ReadFrame(); err != nil {
				return
			}
		}
	}()
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
	enc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"})
	fr.WriteHeaders(http2.HeadersFrameParam{StreamID: streamID, BlockFragment: hb.Bytes(), EndHeaders: true})
	bw.Flush()
	for frames := range cycles {
		for _, p := range frames {
			if err := fr.WriteData(streamID, false, p); err != nil {
				errCh <- err
				return
			}
		}
		if err := bw.Flush(); err != nil {
			errCh <- err
			return
		}
	}
}

// TestVerifyC3_EndToEnd runs the two workloads through the delivered client
// transport: a raw HTTP/2 peer sends each cycle's three DATA frames, the frames
// are allocated by the real framer (readDataFrame), queued/compacted by the
// real recvBuffer, and the application reads each 3-frame message with
// ClientStream.Read and keeps the returned buffers.
func TestVerifyC3_EndToEnd(t *testing.T) {
	for _, w := range verifyC3Workloads {
		t.Run(w.name, func(t *testing.T) {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer lis.Close()
			cycles := make(chan [][]byte)
			defer close(cycles)
			errCh := make(chan error, 4)
			go verifyC3RawServer(lis, cycles, errCh)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})
			if err != nil {
				t.Fatalf("NewHTTP2Client: %v", err)
			}
			defer ct.Close(errors.New("test done"))
			stream, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "/svc/M"}, nil)
			if err != nil {
				t.Fatalf("NewStream: %v", err)
			}
			if _, err := stream.Header(); err != nil {
				t.Fatalf("Header: %v", err)
			}

			var retained mem.BufferSlice
			var want []byte
			compactedCycles := 0
			for i := 0; i < w.cycles; i++ {
				var frames [][]byte
				for j := 0; j < 3; j++ {
					p := verifyC3Pattern(i, j, w.frameSize)
					want = append(want, p...)
					frames = append(frames, p)
				}
				select {
				case cycles <- frames:
				case err := <-errCh:
					t.Fatalf("raw server: %v", err)
				}
				// Wait until all three frames of this message are queued, so
				// that the tail of every message goes through compaction.
				deadline := time.Now().Add(10 * time.Second)
				for {
					_, n := verifyC3Queued(&stream.buf)
					if n == 3*w.frameSize {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("cycle %d: only %d of %d bytes queued", i, n, 3*w.frameSize)
					}
					time.Sleep(20 * time.Microsecond)
				}
				stream.buf.mu.Lock()
				if stream.buf.compactBuf != nil && len(*stream.buf.compactBuf) == 2*w.frameSize {
					compactedCycles++
				}
				stream.buf.mu.Unlock()
				data, err := stream.Read(3 * w.frameSize)
				if err != nil {
					t.Fatalf("cycle %d: Read: %v", i, err)
				}
				retained = append(retained, data...)
			}
			distinct, allocs, got := verifyC3Distinct(retained)
			if !bytes.Equal(got, want) {
				t.Fatalf("payload mismatch: got %d bytes want %d", len(got), len(want))
			}
			limit := 4*len(want) + 64*1024
			verdict := "WITHIN"
			if distinct > limit {
				verdict = "EXCEEDS"
			}
			t.Logf("RESULT e2e %s: cycles whose 2-frame tail sat in the compaction buffer=%d/%d, payload=%d B, distinct backing allocations=%d, retained capacity=%d B, bound=%d B -> %s", w.name, compactedCycles, w.cycles, len(want), allocs, distinct, limit, verdict)
			if distinct > limit {
				t.Errorf("retained capacity %d exceeds bound %d", distinct, limit)
			}
			retained.Free()
		})
	}
}

// TestVerifyC3_HandlerStyleInputsInfo is informational only (never fails): it
// repeats the read-and-retain loop with inputs shaped like
// serverHandlerTransport.HandleStreams produces them (a short slice of a
// pool.Get(http2MaxFrameLen) allocation). The first frame of each cycle is
// handed to the reader directly, before any compaction can apply, so its
// 16 KiB allocation is retained regardless of the compaction design.
func TestVerifyC3_HandlerStyleInputsInfo(t *testing.T) {
	pool := mem.DefaultBufferPool()
	for _, w := range verifyC3Workloads {
		client := &http2Client{bufferPool: pool}
		s := client.newStream(context.Background(), &CallHdr{}, nil)
		b := &s.buf
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		reader := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
		var retained mem.BufferSlice
		total := 0
		for i := 0; i < w.cycles; i++ {
			for j := 0; j < 3; j++ {
				h := pool.Get(http2MaxFrameLen)
				*h = (*h)[:w.frameSize]
				copy(*h, verifyC3Pattern(i, j, w.frameSize))
				b.put(recvMsg{buffer: mem.NewBuffer(h, pool)})
				total += w.frameSize
			}
			for remaining := 3 * w.frameSize; remaining > 0; {
				buf, err := reader.Read(remaining)
				if err != nil {
					t.Fatalf("cycle %d: %v", i, err)
				}
				remaining -= buf.Len()
				retained = append(retained, buf)
			}
		}
		cancel()
		distinct, allocs, _ := verifyC3Distinct(retained)
		t.Logf("INFO handler-style 16KiB-backed inputs %s: payload=%d B, distinct backing allocations=%d, retained capacity=%d B, bound=%d B", w.name, total, allocs, distinct, 4*total+64*1024)
	}
}
