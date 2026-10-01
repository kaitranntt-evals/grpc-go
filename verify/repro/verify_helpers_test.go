//go:build verify

// Shared helpers for the C3/C4/C5 repros; copied next to them by: bash verify/run.sh C3|C4|C5

package transport

import (
	"bytes"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
)

// verifyGet is one BufferPool.Get call observed by verifyTrackingPool.
type verifyGet struct {
	req, cap       int
	ptr            *byte
	fromRecvBuffer bool // a (*recvBuffer) method is on the call stack
}

// verifyTrackingPool wraps a real pool and records every Get and which of the
// returned buffers are still outstanding (not yet Put back).
type verifyTrackingPool struct {
	mem.BufferPool
	mu   sync.Mutex
	gets []verifyGet
	live map[*byte]verifyGet
}

func newVerifyTrackingPool() *verifyTrackingPool {
	return &verifyTrackingPool{BufferPool: mem.DefaultBufferPool(), live: map[*byte]verifyGet{}}
}

func (p *verifyTrackingPool) Get(n int) *[]byte {
	b := p.BufferPool.Get(n)
	g := verifyGet{req: n, cap: cap(*b), ptr: &(*b)[:1][0]}
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "(*recvBuffer)") {
			g.fromRecvBuffer = true
		}
		if !more {
			break
		}
	}
	p.mu.Lock()
	p.gets = append(p.gets, g)
	p.live[g.ptr] = g
	p.mu.Unlock()
	return b
}

func (p *verifyTrackingPool) Put(b *[]byte) {
	p.mu.Lock()
	delete(p.live, &(*b)[:1][0])
	p.mu.Unlock()
	p.BufferPool.Put(b)
}

// recvBufferGets returns the Get calls made by recvBuffer methods, and how
// many of those buffers are still outstanding.
func (p *verifyTrackingPool) recvBufferGets() (gets []verifyGet, liveCount, liveBytes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, g := range p.gets {
		if g.fromRecvBuffer {
			gets = append(gets, g)
		}
	}
	for _, g := range p.live {
		if g.fromRecvBuffer {
			liveCount++
			liveBytes += g.cap
		}
	}
	return gets, liveCount, liveBytes
}

// verifyStartRawServer starts a bare HTTP/2 server. It answers the first
// stream the client opens with gRPC response headers and, once send is
// closed, writes one DATA frame per entry of frameSizes (payload byte j,
// counted across frames, is byte(j)) followed by a PING. delivered is closed
// when the client acks that PING, i.e. after it has processed every DATA
// frame.
func verifyStartRawServer(t *testing.T, frameSizes []int) (addr string, send, delivered chan struct{}) {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	send, delivered = make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() { lis.Close(); <-done })
	pingData := [8]byte{'v', 'e', 'r', 'i', 'f', 'y', '0', '1'}
	go func() {
		defer close(done)
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := io.ReadFull(conn, make([]byte, len(clientPreface))); err != nil {
			t.Errorf("read preface: %v", err)
			return
		}
		fr := http2.NewFramer(conn, conn)
		if err := fr.WriteSettings(); err != nil {
			t.Errorf("write settings: %v", err)
			return
		}
		var streamID uint32
		for streamID == 0 {
			f, err := fr.ReadFrame()
			if err != nil {
				t.Errorf("read frame: %v", err)
				return
			}
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					fr.WriteSettingsAck()
				}
			case *http2.HeadersFrame:
				streamID = f.StreamID
			}
		}
		var hbuf bytes.Buffer
		henc := hpack.NewEncoder(&hbuf)
		henc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
		henc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"})
		if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: streamID, BlockFragment: hbuf.Bytes(), EndHeaders: true}); err != nil {
			t.Errorf("write headers: %v", err)
			return
		}
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			for {
				f, err := fr.ReadFrame()
				if err != nil {
					return
				}
				if p, ok := f.(*http2.PingFrame); ok && p.IsAck() && p.Data == pingData {
					close(delivered)
				}
			}
		}()
		<-send
		j := 0
		for _, n := range frameSizes {
			payload := make([]byte, n)
			for i := range payload {
				payload[i] = byte(j)
				j++
			}
			if err := fr.WriteData(streamID, false, payload); err != nil {
				t.Errorf("write data: %v", err)
				return
			}
		}
		if err := fr.WritePing(false, pingData); err != nil {
			t.Errorf("write ping: %v", err)
			return
		}
		<-readerDone
	}()
	return lis.Addr().String(), send, delivered
}

// verifyLiveHeap returns the live heap after two full collections.
func verifyLiveHeap() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

// verifyCheckPayload fails the test unless got is byte(0), byte(1), ...
func verifyCheckPayload(t *testing.T, got []byte, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("delivered %d bytes, want %d", len(got), want)
	}
	for i, c := range got {
		if c != byte(i) {
			t.Fatalf("payload byte %d = %d, want %d", i, c, byte(i))
		}
	}
}
