// Run (from the root of a grpc-go checkout of this verify branch, after the fetch + worktree step in ../c2_c3_c4_consolidation_test.go): mkdir -p /tmp/wt-2d69d401/verifye2e && cp verify/instrumentation/grpc_e2e_heap/main.go /tmp/wt-2d69d401/verifye2e/main.go && (cd /tmp/wt-2d69d401 && go run ./verifye2e ; GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go run ./verifye2e)
//
// Public-API end-to-end probe: a stock grpc.NewServer() (no transport options)
// whose handler does not read, and a raw HTTP/2 client that sends N one-byte
// DATA frames on one stream. Prints the live-heap growth of the process caused
// by the server buffering those frames.
package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
)

func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func main() {
	const n = 60000 // fits the default 64 KiB stream window
	release := make(chan struct{})
	started := make(chan struct{})
	srv := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, _ grpc.ServerStream) error {
		close(started)
		<-release
		return nil
	}))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go srv.Serve(lis)

	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		panic(err)
	}
	conn.Write([]byte(http2.ClientPreface))
	fr := http2.NewFramer(conn, conn)
	fr.WriteSettings()
	acked := make(chan struct{})
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			if pf, ok := f.(*http2.PingFrame); ok && pf.IsAck() {
				close(acked)
			}
		}
	}()
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, hf := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "http"}, {Name: ":path", Value: "/verify.Svc/Slow"},
		{Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"},
	} {
		enc.WriteField(hf)
	}
	fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true})
	<-started

	// Pre-build the wire bytes so the client side allocates nothing while the
	// server buffers.
	var wire bytes.Buffer
	wfr := http2.NewFramer(&wire, nil)
	for i := 0; i < n; i++ {
		wfr.WriteData(1, false, []byte{byte(i)})
	}
	wfr.WritePing(false, [8]byte{'v'})
	payload := wire.Bytes()

	before := liveHeap()
	if _, err := conn.Write(payload); err != nil {
		panic(err)
	}
	select {
	case <-acked:
	case <-time.After(20 * time.Second):
		panic("no ping ack")
	}
	after := liveHeap()
	runtime.KeepAlive(payload) // keep the pre-built wire bytes live across both samples
	runtime.KeepAlive(&wire)
	growth := int64(after) - int64(before)
	fmt.Printf("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=%q frames=%d payload_bytes=%d live_heap_growth=%d bytes (%.1f bytes/frame)\n",
		os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), n, n, growth, float64(growth)/n)
	close(release)
	conn.Close()
	srv.Stop()
}
