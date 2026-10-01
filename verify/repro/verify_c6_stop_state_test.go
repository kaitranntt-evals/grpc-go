// Run: cp verify/repro/verify_c6_stop_state_test.go internal/xds/server/ && go test -tags verify_audit -race -count=1 -v -run '^Test$/^VerifyC6_StopState$' ./internal/xds/server
//
// C6 "stop-time routing state" probe: a connWrapper retains the filter chain's
// routing-state pointer exactly as listenerWrapper.Accept sets it up; the
// filter chain is then stopped and the state the connection still holds is
// printed.

//go:build verify_audit

package server

import (
	"sync/atomic"
	"testing"
)

func (s) TestVerifyC6_StopState(t *testing.T) {
	fc := &filterChain{usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{}}
	fc.usableRouteConfiguration.Store(&usableRouteConfiguration{}) // a usable (error-free) configuration
	cw := &connWrapper{filterChain: fc, urc: fc.usableRouteConfiguration}

	t.Logf("C6 before filterChain.stop(): routing state held by the existing connection: err=%v", cw.urc.Load().err)
	fc.stop()
	t.Logf("C6 after  filterChain.stop(): routing state held by the existing connection: err=%v", cw.urc.Load().err)
	if err := cw.urc.Load().err; err != nil {
		t.Logf("C6 OBSERVATION: STOPPED-CHAIN-ERROR-PUBLISHED: %q", err.Error())
	} else {
		t.Logf("C6 OBSERVATION: ROUTING-STATE-UNCHANGED-BY-STOP")
	}
}
