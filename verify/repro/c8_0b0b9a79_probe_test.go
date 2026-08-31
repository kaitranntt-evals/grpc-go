//go:build verify_repro

/*
 * Verify probe for claim C8 on branch evalon/grpc-go-xd-0b0b9a79: does
 * repeated delivery of an unchanged effective Cluster security configuration
 * rebuild providers and replace HandshakeInfo snapshots?
 *
 * Run: go test ./internal/xds/balancer/clusterimpl/ -run TestVerifyC8 -count=1 -v
 */

package clusterimpl

import (
	"context"
	"crypto/x509"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type verifyC8Provider struct {
	closeCount int32
}

func (p *verifyC8Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}

func (p *verifyC8Provider) Close() { atomic.AddInt32(&p.closeCount, 1) }

func TestVerifyC8_UnchangedConfigRebuildsProviders(t *testing.T) {
	var buildCount int32
	var providers []*verifyC8Provider

	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, _, _ string, _, _ bool) (certprovider.Provider, error) {
		atomic.AddInt32(&buildCount, 1)
		p := &verifyC8Provider{}
		providers = append(providers, p)
		return p, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{}).(*clusterImplBalancer)
	defer b.Close()
	b.xdsCredsInUse = true
	b.xdsClient = fakeclient.NewClient()

	cfg := &xdsresource.SecurityConfig{RootInstanceName: "root-instance"}

	if err := b.handleSecurityConfig(cfg); err != nil {
		t.Fatalf("first handleSecurityConfig() failed: %v", err)
	}
	firstBuilds := atomic.LoadInt32(&buildCount)
	firstHI := b.xdsHIPtr.Load()

	// Deliver the exact same effective security configuration again.
	if err := b.handleSecurityConfig(cfg); err != nil {
		t.Fatalf("second handleSecurityConfig() failed: %v", err)
	}
	secondBuilds := atomic.LoadInt32(&buildCount)
	secondHI := b.xdsHIPtr.Load()

	t.Logf("provider builds: after first update = %d, after identical second update = %d", firstBuilds, secondBuilds)
	t.Logf("HandshakeInfo replaced by identical update: %v", firstHI != secondHI)
	var closes int32
	for _, p := range providers {
		closes += atomic.LoadInt32(&p.closeCount)
	}
	t.Logf("provider Close() calls after identical second update: %d", closes)

	if secondBuilds > firstBuilds || firstHI != secondHI {
		t.Fatalf("VERDICT CONFIRMED: identical update caused %d additional provider build(s), HandshakeInfo replaced=%v, %d provider closure(s)", secondBuilds-firstBuilds, firstHI != secondHI, closes)
	}
	t.Log("VERDICT REFUTED: identical update caused no rebuild or replacement")
}
