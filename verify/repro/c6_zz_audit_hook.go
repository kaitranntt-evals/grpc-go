// Audit pause hook declaration for C6 (copied by c6_publication_race.sh; not production code).

//go:build ignore

package endpointsharding

// Audit instrumentation: a pause point between the inhibit check and es.mu acquisition in updateState.
var auditAfterInhibitCheck func()
