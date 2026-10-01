//go:build verify

// Run: bash verify/run.sh C1   (copies this file into internal/transport of a worktree of evalon/grpc-go-tr-fbdde71c; runs it unmutated, then with repro/c1_stale_tail_mutation.patch applied)

package transport

import (
	"bytes"
	"context"
	"io"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
)

// TestVerifyC1_SequentialReadThenPut is the kind of test the branch does not
// have: reads are sequenced *between* puts on one live recvBuffer, and the
// payload is asserted across that boundary. It passes on the branch as
// delivered and fails once a "put after read" bug is injected.
func (s) TestVerifyC1_SequentialReadThenPut(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, true)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	var q recvBuffer
	q.init()
	r := recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &q}
	read := func() string {
		buf, err := r.Read(1024)
		if err == io.EOF {
			return "<EOF>"
		}
		if err != nil {
			t.Fatalf("Read() failed: %v", err)
		}
		defer buf.Free()
		return string(buf.ReadOnlyData())
	}
	var got string
	q.put(recvMsg{buffer: mem.SliceBuffer("a")}) // straight to the channel
	q.put(recvMsg{buffer: mem.SliceBuffer("b")}) // compacted into a backlog chunk
	got += read()                                // "a"; load() publishes the chunk
	got += read()                                // "b"; the chunk is now consumed
	q.put(recvMsg{buffer: mem.SliceBuffer("c")}) // later data on the same live buffer
	q.put(recvMsg{buffer: mem.SliceBuffer("d")}) // later data, backlogged again
	q.put(recvMsg{err: io.EOF})                  // terminal transition with "c","d" unread
	for {
		s := read()
		if s == "<EOF>" {
			break
		}
		got += s
	}
	if got != "abcd" {
		t.Fatalf("payload across intermediate reads = %q, want %q", got, "abcd")
	}
}

// TestVerifyC1_ConcurrentReadsSchedule is the branch's
// TestRecvBufferCompactionConcurrentReads, byte-for-byte in behaviour, plus
// two counters that record where the producer was when the consumer issued
// its first read. It shows which producer/consumer orderings the test's only
// synchronization (the `ready` channel) actually admits.
func (s) TestVerifyC1_ConcurrentReadsSchedule(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, true)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	var q recvBuffer
	q.init()
	const payloadSize = 64 * 1024
	want := make([]byte, payloadSize)
	for i := range want {
		want[i] = byte(i)
	}
	var puts atomic.Int64        // data puts completed by the producer
	var eofPut atomic.Bool       // producer has also put the terminal io.EOF
	putsAtFirstRead := int64(-1) // sampled immediately before the first read
	eofAtFirstRead := false
	ready, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i, v := range want {
			q.put(recvMsg{buffer: mem.SliceBuffer{v}})
			puts.Add(1)
			if i == 1024 {
				close(ready)
			}
		}
		q.put(recvMsg{err: io.EOF})
		eofPut.Store(true)
	}()
	defer func() { <-done }()
	<-ready
	r := recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &q}
	var got mem.BufferSlice
	defer func() { got.Free() }()
	for {
		if putsAtFirstRead < 0 {
			eofAtFirstRead = eofPut.Load()
			putsAtFirstRead = puts.Load()
		}
		header := make([]byte, 5)
		n, err := r.ReadMessageHeader(header)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadMessageHeader() failed: %v", err)
		}
		got = append(got, mem.SliceBuffer(header[:n]))
		buf, err := r.Read(17)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read() failed: %v", err)
		}
		got = append(got, buf)
	}
	if data := got.Materialize(); !bytes.Equal(data, want) {
		t.Errorf("received %d bytes, want %d bytes with matching contents", len(data), len(want))
	}
	t.Logf("SCHEDULE putsBeforeFirstRead=%d/%d putsAfterFirstRead=%d eofPutBeforeFirstRead=%v", putsAtFirstRead, payloadSize, payloadSize-putsAtFirstRead, eofAtFirstRead)
}
