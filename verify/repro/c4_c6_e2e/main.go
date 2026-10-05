// Run from the repo root: go run ./verify/repro/c4_c6_e2e 512   (or 1024; optional 2nd arg = DATA frame size, default 64)
//
// End-to-end check for C4/C6: a real grpc.Server served through net/http's
// HTTP/2 server (grpc.Server.ServeHTTP -> serverHandlerTransport), a raw HTTP/2
// peer sending N small DATA frames, and an RPC handler that does not read.
package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/experimental"
	"google.golang.org/grpc/mem"
)

type trackPool struct {
	inner mem.BufferPool
	mu    sync.Mutex
	out   map[*[]byte]int
	gets  map[int]int
}

func (p *trackPool) Get(n int) *[]byte {
	b := p.inner.Get(n)
	p.mu.Lock()
	p.out[b] = cap(*b)
	p.gets[n]++
	p.mu.Unlock()
	return b
}

func (p *trackPool) Put(b *[]byte) {
	p.mu.Lock()
	delete(p.out, b)
	p.mu.Unlock()
	p.inner.Put(b)
}

func heap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func main() {
	n, size := 512, 64
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	if len(os.Args) > 2 {
		size, _ = strconv.Atoi(os.Args[2])
	}
	pool := &trackPool{inner: mem.DefaultBufferPool(), out: map[*[]byte]int{}, gets: map[int]int{}}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	srv := grpc.NewServer(experimental.BufferPool(pool), grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
		started <- struct{}{}
		<-release // slow application: never reads the request stream
		return nil
	}))
	ts := httptest.NewUnstartedServer(srv)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	conn, err := tls.Dial("tcp", ts.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil {
		panic(err)
	}
	fr := http2.NewFramer(conn, conn)
	var wmu sync.Mutex
	wmu.Lock()
	fr.WriteSettings()
	wmu.Unlock()
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			if sf, ok := f.(*http2.SettingsFrame); ok && !sf.IsAck() {
				wmu.Lock()
				fr.WriteSettingsAck()
				wmu.Unlock()
			}
		}
	}()
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, f := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/svc/m"},
		{Name: ":authority", Value: "localhost"}, {Name: "content-type", Value: "application/grpc"}, {Name: "te", Value: "trailers"},
	} {
		enc.WriteField(f)
	}
	wmu.Lock()
	fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true})
	wmu.Unlock()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		panic("handler never started")
	}
	time.Sleep(100 * time.Millisecond)
	before := heap()
	payload := make([]byte, size)
	for i := 0; i < n; i++ {
		wmu.Lock()
		err := fr.WriteData(1, false, payload)
		wmu.Unlock()
		if err != nil {
			panic(err)
		}
		time.Sleep(300 * time.Microsecond) // the peer paces its tiny frames
	}
	time.Sleep(time.Second)
	after := heap()
	pool.mu.Lock()
	cnt, capSum := len(pool.out), 0
	for _, c := range pool.out {
		capSum += c
	}
	gets := fmt.Sprint(pool.gets)
	pool.mu.Unlock()
	fmt.Printf("GOARCH=%s GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=%q\n", runtime.GOARCH, os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"))
	fmt.Printf("sent %d DATA frames x %d bytes = %d payload bytes (%d KiB) to grpc.Server.ServeHTTP; handler not reading\n", n, size, n*size, n*size/1024)
	fmt.Printf("server buffer pool Get calls by requested size: %s\n", gets)
	fmt.Printf("server pool buffers retained (Get without Put): %d, total backing capacity %d bytes (%.2f MiB)\n", cnt, capSum, float64(capSum)/(1<<20))
	fmt.Printf("process live heap growth while frames were queued: %d bytes (%.2f MiB)\n", int64(after)-int64(before), float64(int64(after)-int64(before))/(1<<20))
	close(release)
}
