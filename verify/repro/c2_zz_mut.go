// Audit fault injection for C2 (copied into balancer/endpointsharding by c2_stalled_worker.sh; not production code).

//go:build ignore

package endpointsharding

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Audit instrumentation (not production code).
// ES_STALL=1: a child UpdateClientConnState call that was held by the test
// fixture for more than ES_STALL_MS (default 20ms) never returns afterwards (models a worker stalled
// while still holding the locks the production code holds around that call).
// ES_DROP_EXITIDLE=1: ExitIdle requests are silently dropped (models a
// progress regression so that the tests' progress assertion times out).
func stallIfSlow(f func() error) error {
	start := time.Now()
	err := f()
	if os.Getenv("ES_STALL") == "1" && time.Since(start) > stallThreshold() {
		fmt.Fprintf(os.Stderr, "AUDIT: stalling update worker after held child call (%v)\n", time.Since(start))
		<-make(chan struct{})
	}
	return err
}

func dropExitIdle() bool { return os.Getenv("ES_DROP_EXITIDLE") == "1" }

// ES_STALL_EXITIDLE=1: an idle-exit worker stalls forever inside the child
// ExitIdle call (while still holding the per-child lock taken around it).
func stallExitIdle() {
	if os.Getenv("ES_STALL_EXITIDLE") == "1" {
		fmt.Fprintf(os.Stderr, "AUDIT: stalling idle-exit worker inside child ExitIdle\n")
		<-make(chan struct{})
	}
}

func stallThreshold() time.Duration {
	if ms, err := strconv.Atoi(os.Getenv("ES_STALL_MS")); err == nil {
		return time.Duration(ms) * time.Millisecond
	}
	return 20 * time.Millisecond
}
