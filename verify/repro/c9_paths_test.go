//go:build verify_audit

// C9 repro: copy to internal/transport/ on evalon/grpc-go-tr-9253f1d4, then: go test -tags verify_audit -v -run '^TestC9_' google.golang.org/grpc/internal/transport -race -count=1

package transport

// Probe for C9: instantiate a stream through each production construction path
// (client newStream, http2Server.operateHeaders, serverHandlerTransport.HandleStreams)
// under the default env, queue 1026 one-byte payloads and report the backlog length.
// Run: go test -tags verify_audit -v -run '^TestC9_' google.golang.org/grpc/internal/transport -count=1

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

func c9Fill(t *testing.T, label string, b *recvBuffer) int {
	t.Helper()
	pool := mem.DefaultBufferPool()
	const n = 1026
	for i := 0; i < n; i++ {
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i%251 + 1)}, pool)})
	}
	b.mu.Lock()
	got := len(b.backlog)
	b.mu.Unlock()
	t.Logf("C9 %s: env=%q queued=%d one-byte payloads -> len(backlog)=%d", label, os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), n, got)
	return got
}

func TestC9_ClientNewStream(t *testing.T) {
	s := (&http2Client{bufferPool: mem.DefaultBufferPool()}).newStream(context.Background(), &CallHdr{}, nil)
	if got := c9Fill(t, "http2Client.newStream", &s.buf); got > 64 {
		t.Errorf("client path NOT compacting: backlog=%d", got)
	}
}

func TestC9_HTTP2ServerOperateHeaders(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	streamCh := make(chan *ServerStream, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		st, err := NewServerTransport(conn, &ServerConfig{BufferPool: mem.DefaultBufferPool(), MaxStreams: 10})
		if err != nil {
			t.Errorf("NewServerTransport: %v", err)
			return
		}
		st.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s })
	}()
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatal(err)
	}
	defer ct.Close(context.Canceled)
	if _, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "/svc/m"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-streamCh:
		if got := c9Fill(t, "http2Server.operateHeaders", &s.buf); got > 64 {
			t.Errorf("http2Server path NOT compacting: backlog=%d", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for server stream")
	}
}

// Direct put() on a stream constructed by serverHandlerTransport.HandleStreams.
func TestC9_HandlerServerDirectPut(t *testing.T) {
	st := newHandleStreamTest(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go st.ht.HandleStreams(ctx, func(s *ServerStream) {
		defer close(done)
		if got := c9Fill(t, "serverHandlerTransport.HandleStreams(direct put)", &s.buf); got > 64 {
			t.Errorf("handler path NOT compacting: backlog=%d", got)
		}
	})
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout")
	}
	st.bodyw.Close()
	st.ht.Close(nil)
}

// End to end: 1026 one-byte request-body chunks written through the HTTP
// request body, read by the production Body.Read goroutine into s.buf.
func TestC9_HandlerServerViaRequestBody(t *testing.T) {
	st := newHandleStreamTest(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	streamCh := make(chan *ServerStream, 1)
	go st.ht.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s })
	s := <-streamCh
	const n = 1026
	for i := 0; i < n; i++ {
		if _, err := st.bodyw.Write([]byte{byte(i%251 + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	// Wait for the reader goroutine to enqueue everything.
	deadline := time.Now().Add(5 * time.Second)
	var got, last int
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		s.buf.mu.Lock()
		got = len(s.buf.backlog)
		s.buf.mu.Unlock()
		if got == last && got > 0 {
			break
		}
		last = got
	}
	t.Logf("C9 serverHandlerTransport via request body: wrote %d one-byte chunks -> len(backlog)=%d", n, got)
	if got > 64 {
		t.Errorf("handler path NOT compacting: backlog=%d", got)
	}
	st.bodyw.Close()
	st.ht.Close(nil)
}
