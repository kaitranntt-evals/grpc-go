// Run: cp verify/repro/c4_duplicate_stream_test.go test/ && go test ./test -run '^TestVerifyC4_' -count=1 -v
//
// Registers a hand-written ServiceDesc with two streaming descriptors sharing
// the same StreamName but with different streaming flags and distinguishable
// handlers, then (a) invokes the method and records which handler ran and
// (b) compares that with what GetServiceInfo reports for the method.
package test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestVerifyC4_DuplicateStreamNameDispatchVsServiceInfo(t *testing.T) {
	const serviceName = "verify.DupService"
	ran := make(chan string, 2)
	sd := &grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "Dup",
				ClientStreams: true, // FIRST descriptor: client-streaming
				Handler: func(_ any, ss grpc.ServerStream) error {
					ran <- "FIRST(ClientStreams=true)"
					if err := ss.RecvMsg(&testpb.Empty{}); err != nil {
						return err
					}
					return ss.SendMsg(&testpb.Empty{})
				},
			},
			{
				StreamName:    "Dup",
				ServerStreams: true, // FINAL descriptor: server-streaming
				Handler: func(_ any, ss grpc.ServerStream) error {
					ran <- "FINAL(ServerStreams=true)"
					if err := ss.RecvMsg(&testpb.Empty{}); err != nil {
						return err
					}
					return ss.SendMsg(&testpb.Empty{})
				},
			},
		},
	}

	srv := grpc.NewServer()
	t.Cleanup(srv.Stop)
	srv.RegisterService(sd, nil) // registration must accept the duplicate name for the claim to apply

	var reported []grpc.MethodInfo
	for _, mi := range srv.GetServiceInfo()[serviceName].Methods {
		if mi.Name == "Dup" {
			reported = append(reported, mi)
		}
	}
	t.Logf("GetServiceInfo reports for Dup: %+v", reported)

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)

	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The client half-closes after its single message (ClientStreams=false) so
	// that either server descriptor can complete the RPC.
	stream, err := cc.NewStream(ctx, &grpc.StreamDesc{StreamName: "Dup", ServerStreams: true}, "/"+serviceName+"/Dup")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := stream.SendMsg(&testpb.Empty{}); err != nil {
		t.Fatalf("SendMsg: %v", err)
	}
	var which string
	select {
	case which = <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("no handler ran")
	}
	t.Logf("DISPATCHED handler: %s", which)
	if err := stream.RecvMsg(&testpb.Empty{}); err != nil {
		t.Fatalf("RecvMsg: %v", err)
	}

	// dispatch_retention: the claim holds if the FIRST descriptor's handler ran.
	if which != "FINAL(ServerStreams=true)" {
		t.Errorf("dispatch_retention: dispatch used %s instead of the final descriptor", which)
	}
	// reflection_dispatch_inconsistency: flags reported by GetServiceInfo must
	// match the descriptor that was dispatched.
	if len(reported) != 1 {
		t.Fatalf("GetServiceInfo reported %d entries for Dup, want 1", len(reported))
	}
	dispatchedFlags := grpc.MethodInfo{Name: "Dup", IsClientStream: true}
	if which == "FINAL(ServerStreams=true)" {
		dispatchedFlags = grpc.MethodInfo{Name: "Dup", IsServerStream: true}
	}
	if reported[0] != dispatchedFlags {
		t.Errorf("reflection_dispatch_inconsistency: GetServiceInfo reports %+v but dispatch used descriptor with flags %+v", reported[0], dispatchedFlags)
	}
}
