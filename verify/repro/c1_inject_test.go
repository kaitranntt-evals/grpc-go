//go:build verify_audit

// C1 helper (used by c1_run.sh): bash verify/repro/c1_run.sh <worktree-of-claim-branch> <out-dir>

package transport

// Audit instrumentation for C1 (not part of any solution): replaces the
// heap-growth sample taken while compaction is enabled with $C1_INJECT, if set.

import (
	"os"
	"strconv"

	"google.golang.org/grpc/internal/envconfig"
)

func c1Inject(v int64) int64 {
	s := os.Getenv("C1_INJECT")
	if s == "" || !envconfig.EnableReceiveBufferCompaction {
		return v
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return n
}

func c1InjectU(v uint64) uint64 { return uint64(c1Inject(int64(v))) }
