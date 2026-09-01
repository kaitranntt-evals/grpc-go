// Run: bash verify/repro/c1/run.sh 09997670   (applies the load hook to a worktree of evalon/grpc-go-xd-09997670 and runs this test)
//go:build verifyrepro

package xds

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
)

// c1StoreProvider builds a provider through the certprovider store (as
// clusterimpl does), so that it is the store's singleCloseWrappedProvider which
// implements Hold().
func c1StoreProvider(t *testing.T, name string, inner certprovider.Provider) certprovider.Provider {
	t.Helper()
	bc := certprovider.NewBuildableConfig("c1probe-"+name, []byte(t.Name()), func(certprovider.BuildOptions) certprovider.Provider { return inner })
	p, err := bc.Build(certprovider.BuildOptions{CertName: name, WantRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestC1Probe_09997670_FullReplacementInWindow performs the complete
// replacement (close old providers, then store the new HandshakeInfo — the base
// clusterimpl ordering, unchanged on this branch) after the first Load inside
// CurrentHandshakeInfo and before Hold().
func (s) TestC1Probe_09997670_FullReplacementInWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	rootA, rootB := c1RootA(t), c1RootB(t)
	storeA := c1StoreProvider(t, "A", rootA)
	storeB := c1StoreProvider(t, "B", rootB)
	defer storeB.Close()
	hi1 := xdsinternal.NewHandshakeInfo(storeA, nil, nil, false, "", false, false)
	hi2 := xdsinternal.NewHandshakeInfo(storeB, nil, nil, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)

	var loads []string
	replaced := false
	xdsinternal.C1TestHookAfterLoad = func(hi *xdsinternal.HandshakeInfo) {
		loads = append(loads, c1Name(hi, hi1, hi2))
		if hi == hi1 && !replaced {
			replaced = true
			storeA.Close()
			hiPtr.Store(hi2)
		}
	}
	defer func() { xdsinternal.C1TestHookAfterLoad = nil }()

	err := c1Handshake(t, c1Context(ctx, &hiPtr), ts)
	c1Report(t, err, loads, rootA, rootB)
}

// TestC1Probe_09997670_ProvidersClosedBeforeStore places the handshake's Load
// in the real intermediate state of a clusterimpl replacement on this branch:
// the old providers are already closed but the new HandshakeInfo is not stored
// yet. CurrentHandshakeInfo's two Hold attempts both fail and it returns hi1
// unheld.
func (s) TestC1Probe_09997670_ProvidersClosedBeforeStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	rootA, rootB := c1RootA(t), c1RootB(t)
	storeA := c1StoreProvider(t, "A", rootA)
	storeB := c1StoreProvider(t, "B", rootB)
	defer storeB.Close()
	hi1 := xdsinternal.NewHandshakeInfo(storeA, nil, nil, false, "", false, false)
	hi2 := xdsinternal.NewHandshakeInfo(storeB, nil, nil, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)

	var loads []string
	replaced := false
	xdsinternal.C1TestHookAfterLoad = func(hi *xdsinternal.HandshakeInfo) {
		loads = append(loads, c1Name(hi, hi1, hi2))
		if hi == hi1 && !replaced {
			replaced = true
			storeA.Close() // clusterimpl step 1; step 2 (Store) has not happened yet
		}
	}
	defer func() { xdsinternal.C1TestHookAfterLoad = nil }()

	err := c1Handshake(t, c1Context(ctx, &hiPtr), ts)
	hiPtr.Store(hi2) // clusterimpl step 2
	c1Report(t, err, loads, rootA, rootB)
}

// TestC1Probe_09997670_NoHook needs no instrumentation: clusterimpl on this
// branch closes the old providers (line ~378) before it stores the replacement
// HandshakeInfo (line ~386), so a handshake starting in between selects hi1
// whose provider is already closed.
func (s) TestC1Probe_09997670_NoHook(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	rootA, rootB := c1RootA(t), c1RootB(t)
	storeA := c1StoreProvider(t, "A", rootA)
	storeB := c1StoreProvider(t, "B", rootB)
	defer storeB.Close()
	hi1 := xdsinternal.NewHandshakeInfo(storeA, nil, nil, false, "", false, false)
	hi2 := xdsinternal.NewHandshakeInfo(storeB, nil, nil, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)

	storeA.Close() // clusterimpl step 1 of the replacement
	err := c1Handshake(t, c1Context(ctx, &hiPtr), ts)
	hiPtr.Store(hi2) // clusterimpl step 2 of the replacement
	c1Report(t, err, []string{"(no hook)"}, rootA, rootB)
}
