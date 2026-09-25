// Audit fault injection for C4 (copied by c4_test_hangs.sh; not production code).

//go:build ignore

package endpointsharding

import (
	"fmt"
	"os"
)

// Audit instrumentation (not production code): env-gated fault injection.
func auditOn(name string) bool { return os.Getenv(name) == "1" }

func auditStall(name string) {
	if auditOn(name) {
		fmt.Fprintf(os.Stderr, "AUDIT: %s: stalling ExitIdle worker while holding childMu\n", name)
		<-make(chan struct{})
	}
}
