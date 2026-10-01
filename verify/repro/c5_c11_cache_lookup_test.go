// Run: cp verify/repro/c5_c11_cache_lookup_test.go internal/xds/server/verify_c5_c11_cache_lookup_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./internal/xds/server
//
// Audit repro for the cache-lookup part of C5 and C11 (run v-a332a9ce): after
// the last reference to a cached server filter is released (its Close()
// completes), getOrCreateServerFilterWithMap hands the same closed instance out
// again instead of rebuilding it. The test FAILS when that happens.

package server

import (
	"sync/atomic"
	"testing"

	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
)

type vc5UnitBuilder struct {
	httpfilter.Builder
	built int
}

func (b *vc5UnitBuilder) TypeURLs() []string { return []string{"verify.c5"} }
func (b *vc5UnitBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.built++
	return &vc5UnitFilter{}
}

type vc5UnitFilter struct{ closes atomic.Int32 }

func (f *vc5UnitFilter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	return nil, nil
}
func (f *vc5UnitFilter) Close() { f.closes.Add(1) }

func (s) TestVerifyC5C11_CacheLookupReturnsClosedZeroRefEntry(t *testing.T) {
	cache := make(map[serverFilterKey]*refCountedServerFilter)
	b := &vc5UnitBuilder{}
	key := serverFilterKey{name: "f", typeURLs: "verify.c5"}

	first := getOrCreateServerFilterWithMap(cache, b, key).(*refCountedServerFilter)
	inner := first.ServerFilter.(*vc5UnitFilter)
	t.Logf("after 1st lookup: built=%d refCnt=%d closes=%d", b.built, first.refCnt.Load(), inner.closes.Load())

	first.Close() // release the only reference
	_, cached := cache[key]
	t.Logf("after releasing the last reference: refCnt=%d closes=%d stillCached=%v", first.refCnt.Load(), inner.closes.Load(), cached)

	second := getOrCreateServerFilterWithMap(cache, b, key).(*refCountedServerFilter)
	t.Logf("after 2nd lookup: built=%d sameEntry=%v refCnt=%d closes(of returned filter)=%d", b.built, second == first, second.refCnt.Load(), second.ServerFilter.(*vc5UnitFilter).closes.Load())
	if second == first && inner.closes.Load() > 0 {
		t.Errorf("lookup returned the cached entry whose filter was already closed (builder invoked %d time(s), want 2)", b.built)
	}
}
