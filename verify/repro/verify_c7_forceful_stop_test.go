// Run (needs the harness helpers): cp verify/repro/verify_harness_test.go verify/repro/verify_c7_forceful_stop_test.go test/xds/ && go test -tags verify_audit -race -count=1 -v -run '^Test$/^VerifyC7_' ./test/xds
//
// C7 probe: one RPC is inside an interceptor whose AllowRPC returns only when
// the RPC context is canceled (the client itself never cancels during the
// observation window). The server is then stopped forcefully (Stop()).

//go:build verify_audit

package xds_test

import (
	"context"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/protobuf/types/known/anypb"

	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// vfyStacksMatching returns the goroutine stacks that contain every substring.
func vfyStacksMatching(subs ...string) []string {
	buf := make([]byte, 8<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		ok := true
		for _, s := range subs {
			if !strings.Contains(g, s) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, g)
		}
	}
	return out
}

var vfyFrameRE = regexp.MustCompile(`(?m)^(goroutine .*|[^\t\n].*\(.*\))$`)

// vfyFrames compacts a goroutine stack to its header and function names.
func vfyFrames(stack string, max int) string {
	fr := vfyFrameRE.FindAllString(stack, -1)
	for i, f := range fr {
		if j := strings.LastIndex(f, "("); j > 0 && !strings.HasPrefix(f, "goroutine") {
			fr[i] = f[:j]
		}
	}
	if len(fr) > max {
		fr = fr[:max]
	}
	return strings.Join(fr, "\n        ")
}

func (s) TestVerifyC7_ForcefulStopWithCancellationWaitingInterceptor(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	log := &vfyLog{}
	b := vfyNewBuilder(t, "F", log, 0)
	entered := make(chan struct{}, 1)
	b.allowHook = func(ctx context.Context, ic *vfyInterceptor) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done() // return only when the RPC is canceled
		log.add("INTERCEPTOR %s AllowRPC released by RPC context cancellation: %v", ic, ctx.Err())
	}
	env := vfySetup(t, []*v3httppb.HttpFilter{vfyHTTPFilter(t, "F", b.typeURL, "base")},
		vfyRoute(map[string]*anypb.Any{"F": vfyOverride(t, b.typeURL, false, "gen1")}))

	// The client side of this RPC stays alive for the whole observation window.
	rpcCtx, rpcCancel := context.WithCancel(context.Background())
	defer rpcCancel()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := env.client.EmptyCall(rpcCtx, &testpb.Empty{})
		rpcDone <- err
	}()
	select {
	case <-entered:
	case <-env.ctx.Done():
		t.Fatal("RPC never reached the interceptor")
	}
	log.add("TEST: RPC is inside AllowRPC, waiting for its context to be canceled")

	const window = 8 * time.Second
	log.add("TEST: >>> calling forceful Stop()")
	stopDone := make(chan struct{})
	start := time.Now()
	go func() { env.stopServer(); close(stopDone) }()

	select {
	case <-stopDone:
		log.add("TEST: Stop() returned after %v", time.Since(start).Round(time.Millisecond))
		log.dump(t, "forceful Stop with cancellation-waiting interceptor")
		t.Logf("OBSERVATION(C7): STOP-COMPLETED in %v; interceptor released by cancellation=%v", time.Since(start).Round(time.Millisecond), log.index("released by RPC context cancellation") >= 0)
	case <-time.After(window):
		log.add("TEST: Stop() has NOT returned after %v; AllowRPC released=%v", window, log.index("released by RPC context cancellation") >= 0)
		for _, g := range vfyStacksMatching("listenerWrapper).Close") {
			t.Logf("STACK (listener teardown):\n        %s", vfyFrames(g, 14))
		}
		for _, g := range vfyStacksMatching("RouteAndProcess") {
			t.Logf("STACK (routing call):\n        %s", vfyFrames(g, 10))
		}
		// Break the cycle from outside: cancel the client RPC.
		log.add("TEST: canceling the RPC from the client side to break the wait")
		rpcCancel()
		select {
		case <-stopDone:
			log.add("TEST: Stop() returned only after the client-side cancel (total %v)", time.Since(start).Round(time.Millisecond))
		case <-time.After(10 * time.Second):
			log.add("TEST: Stop() still blocked 10s after client-side cancel")
		}
		log.dump(t, "forceful Stop with cancellation-waiting interceptor")
		t.Logf("OBSERVATION(C7): STOP-BLOCKED: forceful Stop() did not return within %v while AllowRPC waited for cancellation", window)
	}
	select {
	case err := <-rpcDone:
		t.Logf("client RPC result: %v", err)
	case <-time.After(5 * time.Second):
		t.Logf("client RPC still pending")
	}
}
