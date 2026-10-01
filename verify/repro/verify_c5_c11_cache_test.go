// Run: cp verify/repro/verify_c5_c11_cache_test.go internal/xds/server/ && go test -tags verify_audit -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./internal/xds/server
//
// C5/C11 "cache lookup" part: drive a cached refCountedServerFilter to zero
// references (underlying Close runs), then look the same key up again.

//go:build verify_audit

package server

import (
	"testing"

	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
)

type vc5Builder struct {
	httpfilter.Builder
	built int
}

func (*vc5Builder) TypeURLs() []string { return []string{"verify-c5-filter"} }
func (b *vc5Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.built++
	return &vc5Filter{}
}

type vc5Filter struct{ closeCalls int }

func (*vc5Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	return nil, nil
}
func (f *vc5Filter) Close() { f.closeCalls++ }

func (s) TestVerifyC5C11_ClosedCacheEntryLookup(t *testing.T) {
	builder := &vc5Builder{}
	cache := make(map[serverFilterKey]*refCountedServerFilter)
	key := serverFilterKey{name: "f", typeURLs: "verify-c5-filter"}

	first := getOrCreateServerFilterWithMap(cache, builder, key).(*refCountedServerFilter)
	under := first.ServerFilter.(*vc5Filter)
	t.Logf("C5/C11 after 1st lookup:  BuildServerFilter calls=%d refCnt=%d underlying Close calls=%d", builder.built, first.refCnt.Load(), under.closeCalls)

	first.Close() // the only reference is released (e.g. filter disabled on every route)
	_, stillCached := cache[key]
	t.Logf("C5/C11 after last release: refCnt=%d underlying Close calls=%d entry still in cache=%v", first.refCnt.Load(), under.closeCalls, stillCached)

	second := getOrCreateServerFilterWithMap(cache, builder, key).(*refCountedServerFilter)
	t.Logf("C5/C11 after 2nd lookup:  BuildServerFilter calls=%d sameWrapper=%v sameUnderlying=%v refCnt=%d underlying Close calls=%d",
		builder.built, second == first, second.ServerFilter == first.ServerFilter, second.refCnt.Load(), under.closeCalls)
	if second == first && under.closeCalls > 0 {
		t.Logf("C5/C11 OBSERVATION: CLOSED-WRAPPER-RETURNED: lookup incremented (refCnt 0 -> %d) and returned the wrapper whose underlying filter was already closed", second.refCnt.Load())
	} else {
		t.Logf("C5/C11 OBSERVATION: FRESH-OR-LIVE-WRAPPER-RETURNED")
	}

	second.Close()
	t.Logf("C5/C11 after releasing 2nd: refCnt=%d underlying Close calls=%d", second.refCnt.Load(), under.closeCalls)
}
