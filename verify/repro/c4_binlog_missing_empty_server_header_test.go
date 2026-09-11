// Run (on evalon/grpc-go-se-0fb77679): cp verify/repro/c4_binlog_missing_empty_server_header_test.go binarylog/ && go test -tags verifyrepro ./binarylog -run '^Test$/^C4Repro' -count=1 -v; rm binarylog/c4_binlog_missing_empty_server_header_test.go
// The test FAILS when the problem is present (no empty SERVER_HEADER binlog event precedes SERVER_TRAILER) and
// PASSES on the base commit 0c51461d, where the sequence is CLIENT_HEADER, CLIENT_MESSAGE, SERVER_HEADER, SERVER_TRAILER.

//go:build verifyrepro

package binarylog_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/stubserver"

	binlogpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// Probe: unary handler succeeds, but the response exceeds the server's max
// send size so SendMsg fails before any bytes are written. Dump the server
// binlog event sequence.
func (s) TestC4Repro_UnarySendFailurePrePayloadServerHeaderBinlog(t *testing.T) {
	defer testSink.clear()
	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: make([]byte, 64)}}, nil
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.MaxSendMsgSize(8)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatal(err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := testgrpc.NewTestServiceClient(ss.CC).UnaryCall(ctx, &testpb.SimpleRequest{})
	t.Logf("C4: UnaryCall err = %v", err)
	time.Sleep(200 * time.Millisecond)
	got := testSink.logEntries(false)
	var seq []string
	sawEmptyServerHeader := false
	for _, e := range got {
		seq = append(seq, e.Type.String())
		if e.Type == binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_HEADER {
			t.Logf("C4: SERVER_HEADER metadata entries = %d", len(e.GetServerHeader().GetMetadata().GetEntry()))
			if len(e.GetServerHeader().GetMetadata().GetEntry()) == 0 {
				sawEmptyServerHeader = true
			}
		}
		if e.Type == binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_TRAILER {
			t.Logf("C4: SERVER_TRAILER status code = %d msg = %q", e.GetTrailer().GetStatusCode(), e.GetTrailer().GetStatusMessage())
		}
	}
	t.Logf("C4: server binlog sequence = %v", seq)
	t.Logf("C4: empty SERVER_HEADER event present = %v", sawEmptyServerHeader)
	if !sawEmptyServerHeader {
		t.Errorf("C4 CONFIRMED: no empty SERVER_HEADER event before SERVER_TRAILER; sequence = %v", seq)
	}
}
