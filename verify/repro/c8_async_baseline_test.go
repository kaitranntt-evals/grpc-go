// Run (from the root of a grpc-go checkout of this verify branch): git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-c92846ef && git worktree add --detach /tmp/wt-c92846ef FETCH_HEAD && cp verify/repro/c8_async_baseline_test.go /tmp/wt-c92846ef/internal/transport/zz_verify_c8_test.go && (cd /tmp/wt-c92846ef && go test ./internal/transport -run 'TestVerifyC8' -count=1 -v -cpu 1)

package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

type c8Sample struct {
	growth        uint64 // what the branch's helper would return
	signed        int64  // after-before without the clamp in heapGrowth
	rxBefore      uint32 // DATA bytes already received when baseline sampling starts
	rxAtBaseline  uint32 // DATA bytes already received when baseline sampling returns
	rxTotal       uint32 // DATA bytes the server sends in total
	backlogBefore int    // recvBuffer backlog entries when baseline sampling returns
}

// c8Measure is the branch's measureClientTinyDataFrames (recv_buffer_test.go)
// with the same statements in the same order. The only additions are the
// lines marked PROBE, which read state and do not change what is measured. It reuses the branch's own
// runTinyDataFramesServer, liveHeapBytes and heapGrowth.
func c8Measure(t *testing.T, compaction bool, payloadLen int) c8Sample {
	t.Helper()
	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, compaction)

	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	msg := make([]byte, 5+payloadLen)
	binary.BigEndian.PutUint32(msg[1:5], uint32(payloadLen))
	copy(msg[5:], payload)

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("net.Listen() failed: %v", err)
	}
	defer lis.Close()
	processed := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		runTinyDataFramesServer(t, lis, msg, processed)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	copts := ConnectOptions{BufferPool: mem.DefaultBufferPool(), ChannelzParent: channelzSubChannel(t)}
	connectCtx, connectCancel := context.WithTimeout(ctx, 2*time.Second)
	defer connectCancel()
	ct, err := NewHTTP2Client(connectCtx, ctx, resolver.Address{Addr: lis.Addr().String()}, copts, func(GoAwayInfo) {})
	if err != nil {
		t.Fatalf("NewHTTP2Client() failed: %v", err)
	}
	defer func() {
		ct.Close(errors.New("closed by test"))
		<-serverDone
	}()

	cs, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "foo.TinyFrames"}, nil)
	if err != nil {
		t.Fatalf("NewStream() failed: %v", err)
	}
	if _, err := cs.Header(); err != nil {
		t.Fatalf("Header() failed: %v", err)
	}
	rx := func() uint32 { // PROBE
		cs.fc.mu.Lock()
		defer cs.fc.mu.Unlock()
		return cs.fc.pendingData
	}
	out := c8Sample{rxTotal: uint32(len(msg))} // PROBE
	out.rxBefore = rx()                        // PROBE
	before := liveHeapBytes()
	out.rxAtBaseline = rx() // PROBE
	cs.buf.mu.Lock()        // PROBE
	out.backlogBefore = len(cs.buf.backlog)
	cs.buf.mu.Unlock()
	select {
	case <-processed:
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for client to process all DATA frames")
	}
	after := liveHeapBytes()
	out.growth = heapGrowth(before, after)
	out.signed = int64(after) - int64(before) // PROBE

	hdr := make([]byte, 5)
	if err := cs.ReadMessageHeader(hdr); err != nil {
		t.Fatalf("ReadMessageHeader() failed: %v", err)
	}
	if got := int(binary.BigEndian.Uint32(hdr[1:])); got != payloadLen {
		t.Fatalf("Message length = %d, want %d", got, payloadLen)
	}
	data, err := cs.Read(payloadLen)
	if err != nil {
		t.Fatalf("Read(%d) failed: %v", payloadLen, err)
	}
	got := data.Materialize()
	data.Free()
	if !bytes.Equal(got, payload) {
		t.Fatalf("Received payload does not match the payload sent by the server")
	}
	if _, err := cs.Read(1); err != io.EOF {
		t.Fatalf("Read() after message = %v, want %v", err, io.EOF)
	}
	return out
}

// TestVerifyC8_BaselineSampledAfterReception replays the measurement of
// TestClientTransport_TinyDataFramesSlowReader and applies that test's only
// heap assertion (`if enabled*5 > disabled { t.Errorf(...) }`) to it.
//
// The third row of every round runs the "enabled" measurement with compaction
// actually switched off, i.e. what a build with a no-op compaction would
// measure, to show whether the assertion can tell the difference.
func TestVerifyC8_BaselineSampledAfterReception(t *testing.T) {
	const payloadLen = 60000 // same as the branch test
	const rounds = 5
	uncovered, acceptedNoop := 0, 0
	for i := 0; i < rounds; i++ {
		disabled := c8Measure(t, false, payloadLen)
		enabled := c8Measure(t, true, payloadLen)
		noop := c8Measure(t, false, payloadLen)
		show := func(name string, s c8Sample) {
			t.Logf("round %d %-22s DATA bytes received before baseline sampling started=%5d/%d, by the time it returned=%5d/%d (backlog entries=%5d); after-before=%+8d; recorded growth=%7d", i, name, s.rxBefore, s.rxTotal, s.rxAtBaseline, s.rxTotal, s.backlogBefore, s.signed, s.growth)
		}
		show("compaction=false:", disabled)
		show("compaction=true:", enabled)
		show("no-op compaction:", noop)
		accepted := !(enabled.growth*5 > disabled.growth)
		acceptedIfNoop := !(noop.growth*5 > disabled.growth)
		t.Logf("round %d branch assertion `enabled*5 > disabled => Errorf`: disabled=%d enabled=%d -> accepted=%v; with a no-op compaction as the enabled side: disabled=%d enabled=%d -> accepted=%v", i, disabled.growth, enabled.growth, accepted, disabled.growth, noop.growth, acceptedIfNoop)
		if accepted && disabled.rxAtBaseline == disabled.rxTotal && enabled.rxAtBaseline == enabled.rxTotal {
			uncovered++
		}
		if acceptedIfNoop {
			acceptedNoop++
		}
	}
	t.Logf("C8 RESULT: %d of %d rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; %d of %d rounds accepted a no-op compaction", uncovered, rounds, acceptedNoop, rounds)
	if uncovered > 0 {
		t.Errorf("PROBLEM REPRODUCED: the test's heap assertion accepted %d round(s) whose measurements contain none of the receive workload", uncovered)
	}
}
