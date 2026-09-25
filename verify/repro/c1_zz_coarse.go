// Audit mutation for C1: one mutex shared by all children (copied by c1_coarse_lock.sh; not production code).

//go:build ignore

package endpointsharding

import "sync"

// Audit mutation: one mutex shared by all children (original coarse locking).
var coarseMu sync.Mutex
