// Run: cp verify/repro/_verify_c2_rds_error_release_test.go internal/xds/server/verify_c2_rds_error_release_test.go && go test -race -v -count=1 ./internal/xds/server -run '^Test$/^Verify_C2_'   (on the branch under test; FAIL = the RDS error transition did not release the retired configuration's server-filter hold; remove the copy afterwards)

/*
 *
 * Copyright 2026 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/internal/grpcsync"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/protobuf/proto"
)

// Audit probe for C2. It only uses identifiers that exist on the task's base
// commit, so the same file compiles on every claim branch.
//
// One filter chain, one HTTP filter, one owner: if the RDS error transition
// released the retired configuration's hold on the server filter, the filter's
// reference count would reach zero and its Close() would be observed.

type c2Log struct {
	mu     sync.Mutex
	events []string
}

func (l *c2Log) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, fmt.Sprintf(format, args...))
}

func (l *c2Log) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *c2Log) count(prefix string) int {
	n := 0
	for _, e := range l.snapshot() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

type c2Builder struct {
	log     *c2Log
	filters int
}

func (*c2Builder) TypeURLs() []string { return []string{"verify.c2.probe.filter"} }
func (*c2Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return nil, nil
}
func (*c2Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return nil, nil
}
func (*c2Builder) IsTerminal() bool { return false }
func (b *c2Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.filters++
	f := &c2Filter{log: b.log, id: fmt.Sprintf("F%d", b.filters)}
	b.log.add("filter-build %s", f.id)
	return f
}

type c2Filter struct {
	log    *c2Log
	id     string
	icpts  int
	closed atomic.Bool
}

func (f *c2Filter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	f.icpts++
	i := &c2Interceptor{log: f.log, id: fmt.Sprintf("%s/I%d", f.id, f.icpts)}
	f.log.add("icpt-build %s filterAlreadyClosed=%v", i.id, f.closed.Load())
	return i, nil
}

func (f *c2Filter) Close() {
	f.closed.Store(true)
	f.log.add("filter-close %s", f.id)
}

type c2Interceptor struct {
	log *c2Log
	id  string
}

func (*c2Interceptor) AllowRPC(context.Context) error { return nil }
func (i *c2Interceptor) Close()                       { i.log.add("icpt-close %s", i.id) }

type c2FilterConfig struct{ httpfilter.FilterConfig }

func c2Dump(t *testing.T, title string, events []string) {
	t.Helper()
	t.Logf("---- %s (%d events) ----", title, len(events))
	for i, e := range events {
		t.Logf("  #%02d %s", i, e)
	}
}

func c2RefCounts(lw *listenerWrapper) string {
	var parts []string
	for k, f := range lw.httpFilters {
		parts = append(parts, fmt.Sprintf("%s=%d", k.name, f.refCnt.Load()))
	}
	if len(parts) == 0 {
		return "(cache empty)"
	}
	return strings.Join(parts, ",")
}

func c2Setup(nChains int) (*c2Log, *listenerWrapper, *xdsresource.RouteConfigUpdate) {
	log := &c2Log{}
	builder := &c2Builder{log: log}
	const routeName = "route-name"
	var fcs []*filterChain
	for i := 0; i < nChains; i++ {
		fc := &filterChain{
			routeConfigName:          routeName,
			usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
			httpFilters:              []xdsresource.HTTPFilter{{Name: "probe", Filter: builder, Config: c2FilterConfig{}}},
		}
		fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
		fcs = append(fcs, fc)
	}
	lw := &listenerWrapper{
		xdsNodeID:                "node-id",
		closed:                   grpcsync.NewEvent(),
		activeFilterChainManager: &filterChainManager{filterChains: fcs},
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler: &rdsHandler{
			updates: make(map[string]rdsWatcherUpdate),
			cancels: make(map[string]func()),
		},
	}
	prefix := "/"
	rc := &xdsresource.RouteConfigUpdate{
		VirtualHosts: []*xdsresource.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*xdsresource.Route{{
				Prefix:     &prefix,
				ActionType: xdsresource.RouteActionNonForwardingAction,
			}},
		}},
	}
	return log, lw, rc
}

// Success -> ResourceError (data == nil) on the same route name, then
// shutdown of the filter chain manager.
func (s) TestVerify_C2_RDSErrorAfterSuccess_ReleasesFilterHold(t *testing.T) {
	const routeName = "route-name"
	log, lw, rc := c2Setup(1)

	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{data: rc})
	fc := lw.activeFilterChainManager.filterChains[0]
	if err := fc.usableRouteConfiguration.Load().err; err != nil {
		t.Fatalf("successful publication failed: %v", err)
	}
	afterSuccess := log.snapshot()
	c2Dump(t, "after successful RDS publication", afterSuccess)
	t.Logf("OBSERVATION cache refcounts after success: %s", c2RefCounts(lw))

	rdsErr := errors.New("verify: rds resource error after success")
	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{err: rdsErr})
	if got := fc.usableRouteConfiguration.Load().err; !errors.Is(got, rdsErr) {
		t.Fatalf("error configuration not published, urc.err = %v", got)
	}
	afterError := log.snapshot()
	c2Dump(t, "after the RDS error transition completed", afterError)
	icptClosed := log.count("icpt-close")
	filterClosed := log.count("filter-close")
	t.Logf("OBSERVATION after error transition: retired interceptors closed=%d, server filter Close() calls=%d, cache refcounts: %s", icptClosed, filterClosed, c2RefCounts(lw))

	lw.activeFilterChainManager.stop()
	c2Dump(t, "after filterChainManager.stop() (shutdown)", log.snapshot())
	t.Logf("OBSERVATION after shutdown: server filter Close() calls=%d, cache refcounts: %s", log.count("filter-close"), c2RefCounts(lw))

	if filterClosed == 0 {
		t.Errorf("PROBLEM REPRODUCED: the RDS error transition completed without releasing the retired configuration's hold on its server filter (sole owner; filter Close() only happened later: total=%d)", log.count("filter-close"))
	}
}

// Success -> ResourceError -> second ResourceError -> success. Shows whether
// the hold left behind by the error transition is carried along until a later
// successful update.
func (s) TestVerify_C2_RDSErrorAfterSuccess_ThenRecovery(t *testing.T) {
	const routeName = "route-name"
	log, lw, rc := c2Setup(1)

	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{data: rc})
	t.Logf("OBSERVATION cache refcounts after success #1: %s", c2RefCounts(lw))
	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{err: errors.New("verify: error 1")})
	t.Logf("OBSERVATION cache refcounts after error #1:   %s (filter Close() calls=%d)", c2RefCounts(lw), log.count("filter-close"))
	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{err: errors.New("verify: error 2")})
	t.Logf("OBSERVATION cache refcounts after error #2:   %s (filter Close() calls=%d)", c2RefCounts(lw), log.count("filter-close"))
	heldDuringError := log.count("filter-close") == 0
	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{data: rc})
	t.Logf("OBSERVATION cache refcounts after success #2: %s (filter Close() calls=%d)", c2RefCounts(lw), log.count("filter-close"))
	lw.activeFilterChainManager.stop()
	t.Logf("OBSERVATION cache refcounts after shutdown:   %s (filter Close() calls=%d)", c2RefCounts(lw), log.count("filter-close"))
	c2Dump(t, "all events", log.snapshot())
	if heldDuringError {
		t.Errorf("PROBLEM REPRODUCED: the retired configuration's server filter hold stayed unreleased for as long as the route configuration was in the error state")
	}
}
