// Run: cp verify/repro/c2_fatal_before_cleanup_test.go test/ && go test ./test -run '^TestVerifyC2_' -count=1 -v
//
// Reproduces the structure of the candidate test
// TestServerHandwrittenServiceDesc_Dispatch (test/server_pipeline_test.go):
// grpc.NewServer(...) is called, then GetServiceInfo assertions use t.Fatalf,
// and only afterwards is `defer srv.Stop()` registered. This repro forces the
// GetServiceInfo assertion to fail (by expecting a wrong first method) and
// observes through channelz whether the created server was ever stopped.
package test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/channelz"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func c2ServiceDesc() *grpc.ServiceDesc {
	const serviceName = "grpc.testing.TestService"
	return &grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "UnaryCall",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := &testpb.SimpleRequest{}
				if err := dec(in); err != nil {
					return nil, err
				}
				return &testpb.SimpleResponse{}, nil
			},
		}},
		Streams: []grpc.StreamDesc{{
			StreamName:    "UnaryCall",
			ServerStreams: true,
			Handler:       func(any, grpc.ServerStream) error { return nil },
		}},
	}
}

// serverStillRegistered reports whether channelz still lists a server with the
// given ID. grpc.NewServer registers the server; Server.Stop/GracefulStop
// remove the entry.
func serverStillRegistered(id int64) bool {
	return channelz.GetServer(id) != nil
}

// TestVerifyC2_FatalBeforeDeferLeavesServerRunning mirrors the candidate test's
// ordering: NewServer -> fatal GetServiceInfo assertion -> defer srv.Stop().
// The assertion is forced to fail. A cleanup registered before NewServer then
// checks whether Stop ran. Expectation if the claim holds: the server is still
// registered in channelz when the test exits (Stop never called).
func TestVerifyC2_FatalBeforeDeferLeavesServerRunning(t *testing.T) {
	var srvID int64
	t.Cleanup(func() {
		if serverStillRegistered(srvID) {
			t.Logf("OBSERVED: server (channelz id %d) still registered at test exit -> Stop()/GracefulStop() was never called", srvID)
		} else {
			t.Logf("OBSERVED: server (channelz id %d) was stopped before test exit", srvID)
		}
	})

	srv := grpc.NewServer()
	// Take the channelz ID via GetServers: the most recently created server.
	servers, _ := channelz.GetServers(0, 1000)
	for _, s := range servers {
		if s.ID > srvID {
			srvID = s.ID
		}
	}
	srv.RegisterService(c2ServiceDesc(), nil)

	// Same shape as the candidate test's first fatal assertion, but with a
	// deliberately wrong expectation so the fatal path is taken.
	wantFirst := grpc.MethodInfo{Name: "NotTheRealFirstMethod"}
	info, ok := srv.GetServiceInfo()["grpc.testing.TestService"]
	if !ok {
		t.Fatalf("GetServiceInfo() missing service")
	}
	if info.Methods[0] != wantFirst {
		t.Fatalf("GetServiceInfo().Methods[0] = %v, want %v (forced failure: this fatal precedes `defer srv.Stop()`)", info.Methods[0], wantFirst)
	}

	defer srv.Stop() // never reached on the fatal path above
}

// TestVerifyC2_Control_CleanupBeforeFatalStopsServer is the control: the same
// forced fatal, but with cleanup registered immediately after NewServer.
func TestVerifyC2_Control_CleanupBeforeFatalStopsServer(t *testing.T) {
	var srvID int64
	t.Cleanup(func() {
		if serverStillRegistered(srvID) {
			t.Logf("OBSERVED: server (channelz id %d) still registered at test exit -> Stop() never called", srvID)
		} else {
			t.Logf("OBSERVED: server (channelz id %d) was stopped before test exit", srvID)
		}
	})

	srv := grpc.NewServer()
	servers, _ := channelz.GetServers(0, 1000)
	for _, s := range servers {
		if s.ID > srvID {
			srvID = s.ID
		}
	}
	t.Cleanup(srv.Stop)
	srv.RegisterService(c2ServiceDesc(), nil)

	wantFirst := grpc.MethodInfo{Name: "NotTheRealFirstMethod"}
	info := srv.GetServiceInfo()["grpc.testing.TestService"]
	if info.Methods[0] != wantFirst {
		t.Fatalf("GetServiceInfo().Methods[0] = %v, want %v (forced failure)", info.Methods[0], wantFirst)
	}
}
