// Run (needs the harness helpers): cp verify/repro/verify_harness_test.go verify/repro/verify_c6_lds_replacement_test.go test/xds/ && go test -tags verify_audit -race -count=1 -v -run '^Test$/^VerifyC6_' ./test/xds
//
// C6 probe: an RPC is admitted on an existing connection (its server stream
// goroutine is parked in stats.Handler.TagRPC, i.e. before the xDS routing
// interceptor runs), a graceful LDS listener replacement is published through
// the management server, and the RPC is then let through to routing.

//go:build verify_audit

package xds_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type vfyTagGate struct {
	armed   atomic.Bool
	entered chan string
	release chan struct{}
}

func (g *vfyTagGate) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	if strings.HasSuffix(info.FullMethodName, "/EmptyCall") && g.armed.CompareAndSwap(true, false) {
		remote := ""
		if p, ok := peer.FromContext(ctx); ok {
			remote = p.Addr.String()
		}
		g.entered <- remote
		<-g.release
	}
	return ctx
}
func (*vfyTagGate) HandleRPC(context.Context, stats.RPCStats)                         {}
func (*vfyTagGate) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (*vfyTagGate) HandleConn(context.Context, stats.ConnStats)                       {}

func vfyC6Run(t *testing.T, withFilter bool) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	log := &vfyLog{}
	b := vfyNewBuilder(t, "F", log, 0)
	gate := &vfyTagGate{entered: make(chan string, 1), release: make(chan struct{})}

	var filters, filters2 []*v3httppb.HttpFilter
	var overrides map[string]*anypb.Any
	if withFilter {
		filters = []*v3httppb.HttpFilter{vfyHTTPFilter(t, "F", b.typeURL, "lds1")}
		filters2 = []*v3httppb.HttpFilter{vfyHTTPFilter(t, "F", b.typeURL, "lds2")}
	} else {
		// Router-only at first; the replacement Listener adds F so that the
		// moment the replacement becomes active is observable (F gets built).
		filters2 = []*v3httppb.HttpFilter{vfyHTTPFilter(t, "F", b.typeURL, "lds2")}
	}
	env := vfySetup(t, filters, vfyRoute(overrides), grpc.StatsHandler(gate))

	var p1 peer.Peer
	if _, err := env.client.EmptyCall(env.ctx, &testpb.Empty{}, grpc.Peer(&p1)); err != nil {
		t.Fatalf("warm-up RPC failed: %v", err)
	}
	log.add("TEST: warm-up RPC ok on existing connection")

	gate.armed.Store(true)
	type res struct {
		err error
		p   peer.Peer
	}
	done := make(chan res, 1)
	go func() {
		var r res
		_, r.err = env.client.EmptyCall(env.ctx, &testpb.Empty{}, grpc.Peer(&r.p))
		done <- r
	}()
	var remote string
	select {
	case remote = <-gate.entered:
	case <-env.ctx.Done():
		t.Fatal("gated RPC never reached the server")
	}
	log.add("TEST: RPC admitted on the existing connection (client %s); server stream parked before the routing interceptor", remote)

	log.add("TEST: >>> publishing replacement Listener (LDS) through the management server")
	env.setServerHCMFilters(t, filters2)
	if !log.waitFor(5*time.Second, "[lds2] BUILT") {
		t.Fatal("replacement listener configuration never became active")
	}
	time.Sleep(300 * time.Millisecond) // let maybeUpdateFilterChains finish (stop of previous chain + Drain start)
	log.add("TEST: replacement Listener active; letting the admitted RPC proceed to routing")
	close(gate.release)

	var r res
	select {
	case r = <-done:
	case <-env.ctx.Done():
		t.Fatal("gated RPC never returned")
	}
	log.add("TEST: admitted RPC returned code=%v err=%v", status.Code(r.err), r.err)

	// The old connection is still draining gracefully: a fresh RPC succeeds.
	if _, err := env.client.EmptyCall(env.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		log.add("TEST: follow-up RPC failed: %v", err)
	} else {
		log.add("TEST: follow-up RPC ok")
	}
	log.dump(t, "admitted RPC across LDS replacement")
	switch {
	case r.err == nil:
		t.Logf("OBSERVATION(C6 withFilter=%v): ADMITTED-RPC-OK: the admitted RPC on the existing connection completed successfully across the LDS replacement", withFilter)
	case strings.Contains(r.err.Error(), "filter chain stopped"):
		t.Logf("OBSERVATION(C6 withFilter=%v): ADMITTED-RPC-FAILED-STOPPED-CHAIN: code=%v err=%v", withFilter, status.Code(r.err), r.err)
	default:
		t.Logf("OBSERVATION(C6 withFilter=%v): ADMITTED-RPC-FAILED-OTHER: code=%v err=%v", withFilter, status.Code(r.err), r.err)
	}
}

func (s) TestVerifyC6_AdmittedRPCAcrossLDSReplacement_WithFilter(t *testing.T) { vfyC6Run(t, true) }
func (s) TestVerifyC6_AdmittedRPCAcrossLDSReplacement_RouterOnly(t *testing.T) { vfyC6Run(t, false) }
