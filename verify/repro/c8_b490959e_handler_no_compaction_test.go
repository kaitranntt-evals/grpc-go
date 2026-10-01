// How to run (branch evalon/grpc-go-tr-b490959e): cp this file to internal/transport/verify_c8_handler_no_compaction_test.go && go test -v -run '^TestVerifyC8_' google.golang.org/grpc/internal/transport -race -count=1
//
// C8: does the handler transport (grpc.Server.ServeHTTP path) leave tiny queued
// request-body payloads uncompacted because HandleStreams leaves recvBuffer.pool nil?
package transport

import (
	"context"
	"io"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"
)

type vC8Stats struct {
	chanMsgs, backlogEntries, pendingBytes, payload, retainedCap, distinct int
}

func vC8Snapshot(b *recvBuffer) vC8Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	var s vC8Stats
	ptrs := map[unsafe.Pointer]bool{}
	account := func(m recvMsg) {
		if m.buffer == nil {
			return
		}
		d := m.buffer.ReadOnlyData()
		s.payload += len(d)
		s.retainedCap += cap(d)
		ptrs[unsafe.Pointer(unsafe.SliceData(d))] = true
	}
	select {
	case m := <-b.c:
		s.chanMsgs = 1
		account(m)
		b.c <- m
	default:
	}
	for _, m := range b.backlog {
		account(m)
	}
	s.backlogEntries = len(b.backlog)
	if b.pending != nil {
		s.pendingBytes = len(*b.pending)
		s.payload += len(*b.pending)
		s.retainedCap += cap(*b.pending)
		ptrs[unsafe.Pointer(unsafe.SliceData(*b.pending))] = true
	}
	s.distinct = len(ptrs)
	return s
}

// Part "Compaction gate": recvBuffer.put with a nil pool vs. a configured pool.
func TestVerifyC8_PartGate_NilPoolSkipsConsolidation(t *testing.T) {
	const n = 1000
	for _, tc := range []struct {
		name string
		init func(b *recvBuffer)
	}{
		{"init() -> pool == nil", func(b *recvBuffer) { b.init() }},
		{"initWithPool(DefaultBufferPool())", func(b *recvBuffer) { b.initWithPool(mem.DefaultBufferPool()) }},
	} {
		b := &recvBuffer{}
		tc.init(b)
		for i := 0; i < n; i++ {
			b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
		}
		t.Logf("C8 gate: %-36s pool==nil:%-5v after %d one-byte puts: %+v", tc.name, b.pool == nil, n, vC8Snapshot(b))
	}
}

// Parts "Handler initialization" + end-to-end workload: a real
// serverHandlerTransport.HandleStreams fed by repeated one-byte request-body reads
// while the application does not read.
func TestVerifyC8_PartHandlerInit_ShortBodyReadsStaySeparate(t *testing.T) {
	const n = 1000
	st := newHandleStreamTest(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	streamCh := make(chan *ServerStream, 1)
	release := make(chan struct{})
	go st.ht.HandleStreams(ctx, func(s *ServerStream) {
		streamCh <- s
		go func() {
			<-release
			s.WriteStatus(status.New(codes.OK, ""))
		}()
	})
	var s *ServerStream
	select {
	case s = <-streamCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for the handler stream")
	}
	t.Logf("C8 handler init: serverHandlerTransport has a buffer pool: %v; stream recvBuffer.pool == nil: %v", st.ht.bufferPool != nil, s.buf.pool == nil)

	// io.Pipe hands each Write to exactly one Body.Read, i.e. n one-byte reads.
	want := make([]byte, n)
	go func() {
		for i := range want {
			want[i] = byte(i)
			if _, err := st.bodyw.Write(want[i : i+1]); err != nil {
				return
			}
		}
	}()
	var snap vC8Stats
	for deadline := time.Now().Add(10 * time.Second); ; {
		snap = vC8Snapshot(&s.buf)
		if snap.payload == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: stream buffered %d bytes, want %d", snap.payload, n)
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("C8 handler stream after %d one-byte request-body reads, application not reading: %+v", n, snap)
	t.Logf("C8 handler stream: %d queue entries for %d unread bytes; %d distinct source buffers; %d bytes of backing capacity retained (%.0fx the unread payload)",
		snap.chanMsgs+snap.backlogEntries, snap.payload, snap.distinct, snap.retainedCap, float64(snap.retainedCap)/float64(snap.payload))
	if snap.backlogEntries >= n-1 && snap.distinct >= n-1 {
		t.Logf("C8 OBSERVED: handler payloads are queued one entry / one source buffer per read (no compaction)")
	} else {
		t.Logf("C8 NOT OBSERVED: handler payloads were consolidated")
	}

	// The data is still delivered intact.
	got := make([]byte, n)
	if _, err := s.readTo(got); err != nil {
		t.Fatalf("readTo: %v", err)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %d, want %d", i, got[i], want[i])
		}
	}
	close(release)
	st.bodyw.CloseWithError(io.EOF)
}
