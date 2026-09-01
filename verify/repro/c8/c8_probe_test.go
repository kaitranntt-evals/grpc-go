// Run: cp verify/repro/c8/c8_probe_test.go ~/wt/7d3bd828/internal/xds/balancer/clusterimpl/ && (cd ~/wt/7d3bd828 && go test -tags verifyrepro ./internal/xds/balancer/clusterimpl -run 'Test/C8Probe' -count=1 -v)
//go:build verifyrepro

package clusterimpl

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type c8Provider struct {
	name       string
	closeCount atomic.Int32
}

func (p *c8Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *c8Provider) Close() { p.closeCount.Add(1) }

type c8XDSClient struct {
	xdsclient.XDSClient
}

func (c8XDSClient) BootstrapConfig() *bootstrap.Config { return &bootstrap.Config{} }

// TestC8Probe_RootProviderLeakOnIdentityBuildFailure feeds handleSecurityConfig
// a config whose root provider builds successfully and whose identity provider
// fails to build, and reports whether the root provider was closed.
func (s) TestC8Probe_RootProviderLeakOnIdentityBuildFailure(t *testing.T) {
	var built []*c8Provider
	origBuild := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, certName string, wantIdentity, wantRoot bool) (certprovider.Provider, error) {
		if wantIdentity {
			return nil, errors.New("c8: identity provider build failure")
		}
		p := &c8Provider{name: instanceName + "/" + certName}
		built = append(built, p)
		return p, nil
	}
	defer func() { buildProvider = origBuild }()

	b := &clusterImplBalancer{xdsCredsInUse: true, xdsClient: c8XDSClient{}}
	b.xdsHIPtr.Store(nil)
	err := b.handleSecurityConfig(&xdsresource.SecurityConfig{
		RootInstanceName:     "root-inst",
		RootCertName:         "root-cert",
		IdentityInstanceName: "id-inst",
		IdentityCertName:     "id-cert",
	})
	t.Logf("C8PROBE handleSecurityConfig err=%v", err)
	if err == nil {
		t.Fatal("expected identity build failure to be returned")
	}
	if len(built) != 1 {
		t.Fatalf("C8PROBE built %d root providers, want 1", len(built))
	}
	rp := built[0]
	hi := b.xdsHIPtr.Load()
	t.Logf("C8PROBE root provider %s: closeCount=%d; published HandshakeInfo=%v", rp.name, rp.closeCount.Load(), hi)
	if rp.closeCount.Load() == 0 && hi == nil {
		t.Errorf("C8PROBE RESULT: root provider built during the failed update was neither closed nor published (leaked)")
	} else {
		t.Logf("C8PROBE RESULT: root provider was released (closeCount=%d) or published", rp.closeCount.Load())
	}
}
