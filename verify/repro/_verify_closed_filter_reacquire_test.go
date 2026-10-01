// Run: cp verify/repro/_verify_closed_filter_reacquire_test.go internal/xds/server/verify_closed_filter_reacquire_test.go && go test -race -v -count=1 ./internal/xds/server -run '^Test$/^Verify_ClosedCacheEntryReacquired$'   (FAIL = the server filter cache hands out an entry whose last reference was released and whose filter was closed; remove the copy afterwards)

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
	"sync/atomic"
	"testing"

	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/protobuf/proto"
)

// Audit probe for the "closed-entry reacquisition" / "cache reuse mechanism"
// parts of C4 and C9: direct cache lookup after the final release.

type reacqBuilder struct{ built int }

func (*reacqBuilder) TypeURLs() []string { return []string{"verify.reacquire.probe.filter"} }
func (*reacqBuilder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return nil, nil
}
func (*reacqBuilder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return nil, nil
}
func (*reacqBuilder) IsTerminal() bool { return false }
func (b *reacqBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.built++
	return &reacqFilter{}
}

type reacqFilter struct{ closed atomic.Bool }

func (*reacqFilter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	return nil, nil
}
func (f *reacqFilter) Close() { f.closed.Store(true) }

func (s) TestVerify_ClosedCacheEntryReacquired(t *testing.T) {
	cache := make(map[serverFilterKey]*refCountedServerFilter)
	b := &reacqBuilder{}
	key := serverFilterKey{name: "probe", typeURLs: "verify.reacquire.probe.filter"}

	first := getOrCreateServerFilterWithMap(cache, b, key)
	inner := first.(*refCountedServerFilter).ServerFilter.(*reacqFilter)
	t.Logf("OBSERVATION after first acquisition: refCnt=%d closed=%v filtersBuilt=%d", cache[key].refCnt.Load(), inner.closed.Load(), b.built)

	first.Close() // final release
	_, stillCached := cache[key]
	t.Logf("OBSERVATION after final release:     refCnt=%d closed=%v entryStillInCache=%v", first.(*refCountedServerFilter).refCnt.Load(), inner.closed.Load(), stillCached)

	second := getOrCreateServerFilterWithMap(cache, b, key)
	same := second == first
	secondInner := second.(*refCountedServerFilter).ServerFilter.(*reacqFilter)
	t.Logf("OBSERVATION after second acquisition: sameInstance=%v refCnt=%d closed=%v filtersBuilt=%d", same, second.(*refCountedServerFilter).refCnt.Load(), secondInner.closed.Load(), b.built)

	if same && secondInner.closed.Load() {
		t.Errorf("PROBLEM REPRODUCED: lookup re-acquired the cached entry after its final release; the returned server filter is already closed (no new filter was built: filtersBuilt=%d)", b.built)
	}
}
