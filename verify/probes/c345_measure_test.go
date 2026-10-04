//go:build verify_audit

// C3/C4/C5 probe: copy to internal/transport/ on evalon/grpc-go-tr-9253f1d4, then: go test -tags verify_audit -v -run '^TestC345_' google.golang.org/grpc/internal/transport -race -count=1

package transport

// Measurement probe for C3/C4/C5 on evalon/grpc-go-tr-9253f1d4: runs the eval
// fixture's workloads against receive buffers built by the production
// constructors (http2Client.newStream and http2Server.operateHeaders) under the
// default environment and logs queued buffer counts and retained capacity.
// Run: go test -tags verify_audit -v -run '^TestC345_' google.golang.org/grpc/internal/transport -race -count=1

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

type c345Factory func() *recvBuffer

func c345Factories(t *testing.T) map[string]c345Factory {
	pool := mem.DefaultBufferPool()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	streamCh := make(chan *ServerStream, 16)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		st, err := NewServerTransport(conn, &ServerConfig{BufferPool: pool, MaxStreams: 100})
		if err != nil {
			return
		}
		st.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s })
	}()
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: pool}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ct.Close(context.Canceled) })
	return map[string]c345Factory{
		"client(newStream)": func() *recvBuffer {
			return &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf
		},
		"server(operateHeaders)": func() *recvBuffer {
			if _, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "/svc/m"}, nil); err != nil {
				t.Fatal(err)
			}
			return &(<-streamCh).buf
		},
	}
}

// c345State returns queued buffer count (backlog entries + pending tail) and retained capacity.
func c345State(b *recvBuffer) (bufs int, retained int, payload int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.backlog {
		if m.buffer != nil {
			bufs++
			retained += cap(m.buffer.ReadOnlyData())
			payload += m.buffer.Len()
		}
	}
	if b.tail != nil {
		bufs++
		retained += cap(b.tail)
		payload += len(b.tail)
	}
	return
}

func c345Drain(t *testing.T, b *recvBuffer, n int) []byte {
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < n {
		if time.Now().After(deadline) {
			t.Fatalf("drain timeout: %d/%d", len(got), n)
		}
		select {
		case m := <-b.c:
			if m.buffer != nil {
				got = append(got, m.buffer.ReadOnlyData()...)
				m.buffer.Free()
			}
			b.load()
		default:
			b.load()
		}
	}
	return got
}

func TestC345_C3_1025OneByteBehindOccupiedChannel(t *testing.T) {
	pool := mem.DefaultBufferPool()
	for name, f := range c345Factories(t) {
		b := f()
		var want []byte
		for i := 0; i < 1026; i++ { // 1 occupies b.c, 1025 queue behind it
			p := []byte{byte(i%251 + 1)}
			want = append(want, p...)
			b.put(recvMsg{buffer: mem.Copy(p, pool)})
		}
		bufs, retained, payload := c345State(b)
		t.Logf("C3 %s: chan=%d len(backlog)=%d queuedBuffers(backlog+tail)=%d queuedPayload=%d retainedCap=%d (fixture bound: backlog<=%d)", name, len(b.c), len(b.backlog), bufs, payload, retained, 1026/16)
		if bufs > 1026/16 || payload != 1025 {
			t.Errorf("C3 %s: NOT consolidated", name)
		}
		if got := c345Drain(t, b, len(want)); !bytes.Equal(got, want) {
			t.Errorf("C3 %s: payload mismatch", name)
		}
	}
}

func TestC345_C4_SmallAndAlternating(t *testing.T) {
	pool := mem.DefaultBufferPool()
	for name, f := range c345Factories(t) {
		// 1-7 byte frames
		b := f()
		const n = 1024 + 50
		var want []byte
		for i := 0; i < n; i++ {
			p := make([]byte, i%7+1)
			for j := range p {
				p[j] = byte((i*13+j)%251 + 1)
			}
			want = append(want, p...)
			b.put(recvMsg{buffer: mem.Copy(p, pool)})
		}
		bufs, retained, payload := c345State(b)
		t.Logf("C4 small(1-7B) %s: frames=%d payload=%d len(backlog)=%d queuedBuffers=%d queuedPayload=%d retainedCap=%d (bound: backlog<=%d)", name, n, len(want), len(b.backlog), bufs, payload, retained, n/8)
		if bufs > n/8 {
			t.Errorf("C4 small %s: bound exceeded", name)
		}
		if got := c345Drain(t, b, len(want)); !bytes.Equal(got, want) {
			t.Errorf("C4 small %s: payload mismatch", name)
		}

		// alternating 1 byte / 2 KiB
		a := f()
		var wantAlt []byte
		put := func(p []byte) {
			wantAlt = append(wantAlt, p...)
			a.put(recvMsg{buffer: mem.Copy(p, pool)})
		}
		put([]byte{1})
		put([]byte{2})
		for i := 0; i < 24; i++ {
			put([]byte{byte(i + 10)})
			p := make([]byte, 2048)
			for j := range p {
				p[j] = byte((i*17 + j) % 251)
			}
			put(p)
		}
		bufs, retained, payload = c345State(a)
		limit := 4*len(wantAlt) + 64*1024
		t.Logf("C4 alternating(1B/2KiB) %s: payload=%d len(backlog)=%d queuedBuffers=%d queuedPayload=%d retainedCap(backlog+tail)=%d (bound: retained<=%d)", name, len(wantAlt), len(a.backlog), bufs, payload, retained, limit)
		if retained > limit {
			t.Errorf("C4 alternating %s: bound exceeded", name)
		}
		if got := c345Drain(t, a, len(wantAlt)); !bytes.Equal(got, wantAlt) {
			t.Errorf("C4 alternating %s: payload mismatch", name)
		}
	}
}

func TestC345_C5_Cycles(t *testing.T) {
	pool := mem.DefaultBufferPool()
	for name, f := range c345Factories(t) {
		b := f()
		var want, got []byte
		for cycle := 0; cycle < 3; cycle++ {
			for i := 0; i < 1124; i++ {
				p := make([]byte, i%5+1)
				for j := range p {
					p[j] = byte((cycle*31+i*7+j)%251 + 1)
				}
				want = append(want, p...)
				b.put(recvMsg{buffer: mem.Copy(p, pool)})
			}
			bufs, retained, payload := c345State(b)
			t.Logf("C5 burst %s cycle %d: len(backlog)=%d queuedBuffers=%d queuedPayload=%d retainedCap=%d (bounds: backlog<=512; 4*payload+64KiB=%d)", name, cycle, len(b.backlog), bufs, payload, retained, 4*payload+64*1024)
			if len(b.backlog) > 512 || bufs > 512 || retained > 4*payload+64*1024 {
				t.Errorf("C5 burst %s cycle %d: bound exceeded", name, cycle)
			}
			b.load()
			select {
			case m := <-b.c:
				got = append(got, m.buffer.ReadOnlyData()...)
				m.buffer.Free()
			default:
			}
		}
		got = append(got, c345Drain(t, b, len(want)-len(got))...)
		if !bytes.Equal(got, want) {
			t.Errorf("C5 burst %s: payload mismatch", name)
		}

		// incremental retention
		a := f()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		reader := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: a}
		var retained mem.BufferSlice
		var wantI, gotI []byte
		total := 0
		for i := 0; i < 1024; i++ {
			for j := 0; j < 3; j++ {
				p := []byte{byte((i*7+j)%251 + 1)}
				a.put(recvMsg{buffer: mem.Copy(p, pool)})
				wantI = append(wantI, p...)
				total++
			}
			for remaining := 3; remaining > 0; {
				buf, err := reader.Read(remaining)
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				remaining -= buf.Len()
				gotI = append(gotI, buf.ReadOnlyData()...)
				retained = append(retained, buf)
			}
		}
		distinct, maxCap := 0, 0
		seen := map[uintptr]bool{}
		for _, buf := range retained {
			d := buf.ReadOnlyData()
			if len(d) > 0 {
				p := uintptr(unsafe.Pointer(unsafe.SliceData(d[:cap(d)])))
				_ = p
				ptr := uintptr(unsafe.Pointer(&d[0]))
				if !seen[ptr] {
					seen[ptr] = true
					distinct += cap(d)
					if cap(d) > maxCap {
						maxCap = cap(d)
					}
				}
			}
		}
		limit := 4*total + 64*1024
		t.Logf("C5 incremental %s: payload=%d retainedBuffers=%d distinctRetainedCap=%d maxSingleCap=%d (bound: <=%d) inOrder=%v", name, total, len(retained), distinct, maxCap, limit, bytes.Equal(gotI, wantI))
		if distinct > limit || !bytes.Equal(gotI, wantI) {
			t.Errorf("C5 incremental %s: bound exceeded or mismatch", name)
		}
		retained.Free()
		cancel()
	}
}
