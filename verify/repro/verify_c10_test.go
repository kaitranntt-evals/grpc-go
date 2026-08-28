// Repro for C10: copy into test/ of branch evalon/grpc-go-se-b1f3fdd3, then: go test ./test -run TestVerifyC10 -count=1 -v
package test

import (
	"testing"

	"google.golang.org/grpc"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
)

func TestVerifyC10GetServiceInfoOrder(t *testing.T) {
	interleaved := 0
	const iters = 200
	for i := 0; i < iters; i++ {
		srv := grpc.NewServer()
		testgrpc.RegisterTestServiceServer(srv, &testgrpc.UnimplementedTestServiceServer{})
		info := srv.GetServiceInfo()
		methods := info["grpc.testing.TestService"].Methods
		seenStream := false
		for _, m := range methods {
			isStream := m.IsClientStream || m.IsServerStream
			if isStream {
				seenStream = true
			} else if seenStream {
				interleaved++
				if interleaved == 1 {
					t.Logf("iteration %d: streaming method precedes unary method: %+v", i, methods)
				}
				break
			}
		}
		srv.Stop()
	}
	t.Logf("interleaved orderings observed: %d/%d", interleaved, iters)
	if interleaved == 0 {
		t.Log("no interleaving observed: all unary methods always preceded streaming methods")
	}
}
