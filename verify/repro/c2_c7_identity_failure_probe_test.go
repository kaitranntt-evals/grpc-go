//go:build verify_repro

/*
 * Verify probe for claim C2/C7: when root-provider construction succeeds and
 * identity-provider construction fails, is the newly created root provider
 * closed before handleSecurityConfig returns?
 *
 * Run: copy to internal/xds/balancer/clusterimpl/ and
 *   go test ./internal/xds/balancer/clusterimpl/ -run TestVerifyC2 -count=1 -v
 */

package clusterimpl

import (
	"context"
	"crypto/x509"
	"errors"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type verifyCountingProvider struct {
	closeCount int32
}

func (p *verifyCountingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}

func (p *verifyCountingProvider) Close() { atomic.AddInt32(&p.closeCount, 1) }

func TestVerifyC2_IdentityBuildFailureRootProviderClose(t *testing.T) {
	root := &verifyCountingProvider{}

	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, _ string, _, _ bool) (certprovider.Provider, error) {
		if instanceName == "root-ok" {
			return root, nil
		}
		return nil, errors.New("verify: identity provider construction failure")
	}
	defer func() { buildProvider = origBuildProvider }()

	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{}).(*clusterImplBalancer)
	defer b.Close()
	b.xdsCredsInUse = true
	b.xdsClient = fakeclient.NewClient()

	err := b.handleSecurityConfig(&xdsresource.SecurityConfig{
		RootInstanceName:     "root-ok",
		IdentityInstanceName: "identity-bad",
		IdentityCertName:     "cert",
	})
	if err == nil {
		t.Fatal("handleSecurityConfig() succeeded, want identity build error")
	}
	t.Logf("handleSecurityConfig() returned error as expected: %v", err)

	got := atomic.LoadInt32(&root.closeCount)
	t.Logf("root provider Close() call count after identity build failure: %d", got)
	if got == 0 {
		t.Fatal("VERDICT: root provider was NOT closed after identity construction failure (leak confirmed)")
	}
	t.Logf("VERDICT: root provider was closed %d time(s) (no leak)", got)
}
