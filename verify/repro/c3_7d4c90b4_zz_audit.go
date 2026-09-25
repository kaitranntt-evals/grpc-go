// Audit lock-trace helper for C3 on 7d4c90b4 (copied by c3_lock_trace.sh; not production code).

//go:build ignore

package endpointsharding

import (
	"runtime"
	"strings"
)

// auditFrames returns the endpointsharding frames of the current goroutine.
func auditFrames() string {
	pc := make([]uintptr, 32)
	n := runtime.Callers(3, pc)
	frames := runtime.CallersFrames(pc[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "endpointsharding") || strings.Contains(f.Function, "stub") {
			b.WriteString("    " + f.Function + "\n")
		}
		if !more {
			break
		}
	}
	return b.String()
}
