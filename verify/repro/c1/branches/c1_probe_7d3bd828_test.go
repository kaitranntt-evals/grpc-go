// Run: bash verify/repro/c1/run.sh 7d3bd828   (applies the load hook to a worktree of evalon/grpc-go-xd-7d3bd828 and runs this test)
//go:build verifyrepro

package xds

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
)

// TestC1Probe_7d3bd828 forces a Cluster security-configuration replacement in the
// window after ClientHandshake has loaded its HandshakeInfo snapshot (hi1, roots
// A) and before it tries to secure ownership of it. The replacement is performed
// exactly the way clusterimpl on this branch performs it.
func (s) TestC1Probe_7d3bd828(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	rootA, rootB := c1RootA(t), c1RootB(t)
	hi1 := xdsinternal.NewHandshakeInfoWithOwnedProviders(rootA, nil, nil, "", false, false)
	hi2 := xdsinternal.NewHandshakeInfoWithOwnedProviders(rootB, nil, nil, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)

	var loads []string
	replaced := false
	xdsinternal.C1TestHookAfterLoad = func(hi *xdsinternal.HandshakeInfo) {
		loads = append(loads, c1Name(hi, hi1, hi2))
		if hi == hi1 && !replaced {
			replaced = true
			// clusterimpl replacement on this branch:
			if old := hiPtr.Swap(hi2); old != nil {
				old.Release()
			}
		}
	}
	defer func() { xdsinternal.C1TestHookAfterLoad = nil }()

	err := c1Handshake(t, c1Context(ctx, &hiPtr), ts)
	c1Report(t, err, loads, rootA, rootB)
}
