// Run: git worktree add ../wt-b <remote>/evalon/grpc-go-xd-f02ebbb2 && cp verify/repro/c4_reader_blocks_later_chain_test.go ../wt-b/internal/xds/server/ && (cd ../wt-b && go test -tags verify_repro -race -count=1 -v -run 'Test/VerifyC4' ./internal/xds/server) -- FAIL with 'CONFIRMED' lines reproduces C4.

//go:build verify_repro

package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/transport"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/metadata"
)

type verifyC4Log struct {
	mu     sync.Mutex
	start  time.Time
	events []string
}

func (l *verifyC4Log) add(t *testing.T, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := fmt.Sprintf("+%4dms %s", time.Since(l.start).Milliseconds(), fmt.Sprintf(format, args...))
	l.events = append(l.events, e)
	t.Log(e)
}

// verifyC4Filter builds interceptors numbered by generation. Only the
// interceptor for which hold != nil blocks in AllowRPC. Close never blocks.
type verifyC4Filter struct {
	httpfilter.Builder
	t     *testing.T
	log   *verifyC4Log
	chain string
	gen   atomic.Int32
	hold  func(gen int32) (entered chan<- struct{}, release <-chan struct{})
}

func (f *verifyC4Filter) TypeURLs() []string                         { return []string{f.chain} }
func (f *verifyC4Filter) IsTerminal() bool                           { return false }
func (f *verifyC4Filter) BuildServerFilter() httpfilter.ServerFilter { return f }
func (f *verifyC4Filter) Close()                                     {}
func (f *verifyC4Filter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	g := f.gen.Add(1)
	f.log.add(f.t, "%s: interceptor gen%d built", f.chain, g)
	return &verifyC4Interceptor{f: f, gen: g}, nil
}

type verifyC4Interceptor struct {
	f   *verifyC4Filter
	gen int32
}

func (i *verifyC4Interceptor) AllowRPC(ctx context.Context) error {
	i.f.log.add(i.f.t, "%s: AllowRPC served by gen%d", i.f.chain, i.gen)
	if i.f.hold != nil {
		if entered, release := i.f.hold(i.gen); release != nil {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			i.f.log.add(i.f.t, "%s: held AllowRPC reader on gen%d released", i.f.chain, i.gen)
		}
	}
	return nil
}

// Close is nonblocking.
func (i *verifyC4Interceptor) Close() {
	i.f.log.add(i.f.t, "%s: interceptor gen%d Close() called (returns immediately)", i.f.chain, i.gen)
}

type verifyC4Stream struct{ grpc.ServerTransportStream }

func (verifyC4Stream) Method() string { return "/service/method" }

func verifyC4RPC(ctx context.Context, fc *filterChain) error {
	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
	ctx = grpc.NewContextWithServerTransportStream(ctx, verifyC4Stream{})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "server"))
	return RouteAndProcess(ctx)
}

func verifyC4Setup(t *testing.T) (l *listenerWrapper, fcA, fcB *filterChain, log *verifyC4Log, entered, release chan struct{}, update rdsWatcherUpdate) {
	log = &verifyC4Log{start: time.Now()}
	entered, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	fA := &verifyC4Filter{t: t, log: log, chain: "chainA", hold: func(gen int32) (chan<- struct{}, <-chan struct{}) {
		var e chan<- struct{}
		var r <-chan struct{}
		if gen == 1 {
			once.Do(func() { e, r = entered, release })
		}
		return e, r
	}}
	fB := &verifyC4Filter{t: t, log: log, chain: "chainB"}
	mk := func(f *verifyC4Filter) *filterChain {
		fc := &filterChain{
			httpFilters:              []xdsresource.HTTPFilter{{Name: f.chain, Filter: f}},
			routeConfigName:          "route",
			usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
		}
		fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
		return fc
	}
	fcA, fcB = mk(fA), mk(fB)
	l = &listenerWrapper{
		activeFilterChainManager: &filterChainManager{filterChains: []*filterChain{fcA, fcB}},
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler:               &rdsHandler{},
	}
	prefix := "/"
	update = rdsWatcherUpdate{data: &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{
		Domains: []string{"*"},
		Routes:  []*xdsresource.Route{{Prefix: &prefix, ActionType: xdsresource.RouteActionNonForwardingAction}},
	}}}}
	l.handleRDSUpdate("route", update) // generation 1 on both chains
	return
}

// Part "reader-dependent retirement": updateRoutingConfiguration on one chain
// does not return while an AllowRPC reader holds the retired configuration.
func (s) TestVerifyC4_ReaderDependentRetirement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, fcA, _, log, entered, release, _ := verifyC4Setup(t)

	rpcDone := make(chan error, 1)
	go func() { rpcDone <- verifyC4RPC(ctx, fcA) }()
	<-entered

	old := fcA.usableRouteConfiguration.Load()
	replacement := &usableRouteConfiguration{}
	updateDone := make(chan struct{})
	go func() {
		log.add(t, "updateRoutingConfiguration(chainA) called")
		fcA.updateRoutingConfiguration(replacement)
		log.add(t, "updateRoutingConfiguration(chainA) returned")
		close(updateDone)
	}()

	select {
	case <-updateDone:
		t.Fatalf("REFUTED: update call returned while the AllowRPC reader was still held")
	case <-time.After(2 * time.Second):
		log.add(t, "2s later: update call still blocked; published=%v, old.closed=%v", fcA.usableRouteConfiguration.Load() == replacement, old.closed)
	}
	close(release)
	select {
	case <-updateDone:
	case <-ctx.Done():
		t.Fatal("update never returned")
	}
	if err := <-rpcDone; err != nil {
		t.Fatalf("held RPC failed: %v", err)
	}
	t.Errorf("CONFIRMED (reader-dependent retirement): updateRoutingConfiguration blocked >=2s on a held AllowRPC reader although interceptor Close() is nonblocking")
}

// Part "sequential publication trigger" + combined behavior: handleRDSUpdate
// for a route used by two active chains does not publish to chain B until
// chain A's update call returns, i.e. until chain A's reader is released.
func (s) TestVerifyC4_LaterChainWaitsForFirstChainReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	l, fcA, fcB, log, entered, release, update := verifyC4Setup(t)
	oldA, oldB := fcA.usableRouteConfiguration.Load(), fcB.usableRouteConfiguration.Load()

	rpcDone := make(chan error, 1)
	go func() { rpcDone <- verifyC4RPC(ctx, fcA) }()
	<-entered

	updateDone := make(chan struct{})
	go func() {
		log.add(t, "handleRDSUpdate(route) called (generation 2)")
		l.handleRDSUpdate("route", update)
		log.add(t, "handleRDSUpdate(route) returned")
		close(updateDone)
	}()

	time.Sleep(2 * time.Second)
	aNew := fcA.usableRouteConfiguration.Load() != oldA
	bNew := fcB.usableRouteConfiguration.Load() != oldB
	log.add(t, "2s later, reader still held: chainA replacement published=%v, chainB replacement published=%v", aNew, bNew)
	// An RPC on chain B during the hold: which generation serves it?
	if err := verifyC4RPC(ctx, fcB); err != nil {
		t.Fatalf("chain B RPC failed: %v", err)
	}
	locked := l.mu.TryLock()
	if locked {
		l.mu.Unlock()
	}
	log.add(t, "listener mutex (needed by Accept to admit new connections) free=%v", locked)
	select {
	case <-updateDone:
		t.Fatalf("handleRDSUpdate returned while reader held")
	default:
	}

	close(release)
	select {
	case <-updateDone:
	case <-ctx.Done():
		t.Fatal("handleRDSUpdate never returned")
	}
	log.add(t, "after release: chainB replacement published=%v", fcB.usableRouteConfiguration.Load() != oldB)
	if err := verifyC4RPC(ctx, fcB); err != nil {
		t.Fatalf("chain B RPC failed: %v", err)
	}
	if err := <-rpcDone; err != nil {
		t.Fatalf("held RPC failed: %v", err)
	}
	if bNew {
		t.Fatalf("REFUTED: chain B replacement was published while chain A's reader was held")
	}
	t.Errorf("CONFIRMED (sequential publication + combined): chain B stayed on its old configuration (aPublished=%v, bPublished=%v, listenerMuFree=%v) until chain A's AllowRPC reader was released", aNew, bNew, locked)
}
