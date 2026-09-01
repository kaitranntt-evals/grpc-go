// Run: bash verify/repro/c1/run.sh 2c037706   (applies the load hook to a worktree of evalon/grpc-go-xd-2c037706 and runs this test)
//go:build verifyrepro

package xds

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
)

// TestC1Probe_2c037706 forces a Cluster security-configuration replacement in the
// window after ClientHandshake has loaded its HandshakeInfo snapshot (hi1, roots
// A) and before it tries to secure ownership of it. The replacement is performed
// exactly the way clusterimpl on this branch performs it.
func (s) TestC1Probe_2c037706(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	rootA, rootB := c1RootA(t), c1RootB(t)
	wrappedA := xdsinternal.NewRefCountedProvider(rootA)
	wrappedB := xdsinternal.NewRefCountedProvider(rootB)
	hi1 := xdsinternal.NewHandshakeInfo(wrappedA, nil, nil, false, "", false, false)
	hi2 := xdsinternal.NewHandshakeInfo(wrappedB, nil, nil, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)

	var loads []string
	replaced := false
	c1TestHookAfterLoad = func(hi *xdsinternal.HandshakeInfo) {
		loads = append(loads, c1Name(hi, hi1, hi2))
		if hi == hi1 && !replaced {
			replaced = true
			// clusterimpl replacement on this branch:
			hiPtr.Store(hi2)
			wrappedA.Close()
		}
	}
	defer func() { c1TestHookAfterLoad = nil }()

	err := c1Handshake(t, c1Context(ctx, &hiPtr), ts)
	c1Report(t, err, loads, rootA, rootB)
}
