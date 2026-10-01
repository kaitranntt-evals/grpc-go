//go:build verify

// Run: bash verify/run.sh C2   (copies this file into internal/transport of a worktree of evalon/grpc-go-tr-5acb5ef2 and runs it with the env var =false and unset)

package transport

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/status"
)

// verifyC2Snapshot describes a production-created recvBuffer after n one-byte
// DATA frames were received while the application was not reading.
type verifyC2Snapshot struct {
	poolNil       bool
	compactedNil  bool
	entries       int
	oneByteEntrys int
	maxEntryLen   int
}

func verifyC2Snap(b *recvBuffer) verifyC2Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := verifyC2Snapshot{poolNil: b.pool == nil, compactedNil: b.compacted == nil, entries: len(b.backlog)}
	for _, m := range b.backlog {
		if m.buffer == nil {
			continue
		}
		if m.buffer.Len() == 1 {
			s.oneByteEntrys++
		}
		s.maxEntryLen = max(s.maxEntryLen, m.buffer.Len())
	}
	return s
}

// The compaction switch is read only from the real process environment here:
// nothing in this test overrides envconfig, and every recvBuffer inspected was
// created by production code (http2Client.newStream, http2Server.operateHeaders,
// serverHandlerTransport.HandleStreams).
func (s) TestVerifyC2_ProductionRecvBuffers(t *testing.T) {
	raw, set := os.LookupEnv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION")
	t.Logf("process env GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=%q (set=%v); envconfig.EnableReceiveBufferCompaction=%v", raw, set, envconfig.EnableReceiveBufferCompaction)
	const n = 3000

	// --- client: http2Client.newStream ---
	addr, start := startTinyFrameServer(t, n, 1)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: addr}, ConnectOptions{BufferPool: mem.DefaultBufferPool(), ChannelzParent: channelzSubChannel(t)}, func(GoAwayInfo) {})
	if err != nil {
		t.Fatalf("NewHTTP2Client: %v", err)
	}
	defer ct.Close(errors.New("closed manually by test"))
	cs, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "foo"}, nil)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if _, err := cs.Header(); err != nil {
		t.Fatalf("Header: %v", err)
	}
	close(start)
	for {
		cs.buf.mu.Lock()
		ended := cs.buf.err != nil
		cs.buf.mu.Unlock()
		if ended {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for stream end")
		case <-time.After(time.Millisecond):
		}
	}
	snap := verifyC2Snap(&cs.buf)
	t.Logf("CLIENT  http2Client stream after %d one-byte DATA frames + trailers, unread: buf.pool==nil:%v buf.compacted==nil:%v backlog entries=%d (one-byte entries=%d, largest entry=%d bytes)", n, snap.poolNil, snap.compactedNil, snap.entries, snap.oneByteEntrys, snap.maxEntryLen)
	data, err := cs.Read(n)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := data.Materialize()
	data.Free()
	for i, c := range got {
		if c != byte(i) {
			t.Fatalf("client payload byte %d = %d, want %d", i, c, byte(i))
		}
	}
	t.Logf("CLIENT  all %d bytes delivered in order", len(got))

	// --- server: http2Server.operateHeaders ---
	server, ct2, cancel2 := setUp(t, 0, suspended)
	defer cancel2()
	defer server.stop()
	defer ct2.Close(errors.New("closed manually by test"))
	cs2, err := ct2.NewStream(ctx, &CallHdr{Host: "localhost", Method: "foo.Small"}, nil)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := cs2.Write(nil, newBufferSlice([]byte{byte(i)}), &WriteOptions{}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	var ss *ServerStream
	deadline := time.Now().Add(defaultTestTimeout)
	var ssnap verifyC2Snapshot
	var queuedBytes int
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for server to queue %d bytes (got %d)", n-1, queuedBytes)
		}
		if ss == nil {
			server.mu.Lock()
			for st := range server.conns {
				h2 := st.(*http2Server)
				h2.mu.Lock()
				for _, v := range h2.activeStreams {
					ss = v
				}
				h2.mu.Unlock()
			}
			server.mu.Unlock()
		}
		if ss != nil {
			ss.buf.mu.Lock()
			queuedBytes = 0
			for _, m := range ss.buf.backlog {
				queuedBytes += m.buffer.Len()
			}
			if ss.buf.compacted != nil {
				queuedBytes += len(*ss.buf.compacted)
			}
			ss.buf.mu.Unlock()
			if queuedBytes >= n-1 { // the first byte sits in the channel
				ssnap = verifyC2Snap(&ss.buf)
				break
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("SERVER  http2Server stream after %d one-byte DATA frames, unread: buf.pool==nil:%v buf.compacted==nil:%v backlog entries=%d (one-byte entries=%d, largest entry=%d bytes)", n, ssnap.poolNil, ssnap.compactedNil, ssnap.entries, ssnap.oneByteEntrys, ssnap.maxEntryLen)

	// --- handler transport: serverHandlerTransport.HandleStreams ---
	hst := newHandleStreamTest(t, nil)
	handlerPoolNil := false
	hst.ht.HandleStreams(context.Background(), func(s *ServerStream) {
		handlerPoolNil = s.buf.pool == nil
		go func() {
			hst.bodyw.Close()
			s.WriteStatus(status.New(codes.OK, ""))
		}()
	})
	t.Logf("HANDLER serverHandlerTransport stream: buf.pool==nil:%v", handlerPoolNil)

	wantLegacy := !envconfig.EnableReceiveBufferCompaction
	if wantLegacy {
		if !snap.poolNil || snap.entries != n || snap.oneByteEntrys != n-1 || !ssnap.poolNil || ssnap.entries != n-1 || ssnap.oneByteEntrys != n-1 || !handlerPoolNil {
			t.Errorf("VERDICT: compaction still active with the opt-out set")
		} else {
			t.Logf("VERDICT: opt-out honoured - production receive buffers are legacy FIFO (one backlog entry per DATA frame, no compaction pool)")
		}
	} else {
		if snap.poolNil || snap.entries > 3 || ssnap.poolNil || handlerPoolNil {
			t.Errorf("VERDICT: compaction not active although enabled")
		} else {
			t.Logf("VERDICT: compaction active (control run)")
		}
	}
}
