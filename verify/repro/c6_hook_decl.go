// Instrumentation for verify/repro/c6_deterministic_interleaving_test.go; copy next to endpointsharding.go after applying c6_widen_window.patch.

package endpointsharding

// VerifyC6AfterInhibitCheck, when set, runs in updateState after the
// inhibition check has passed and before es.mu is acquired. It only widens the
// existing window; it does not change any decision.
var VerifyC6AfterInhibitCheck func()
