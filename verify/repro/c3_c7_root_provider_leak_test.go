// Run: cp this file into internal/xds/balancer/clusterimpl/ of the target branch (ce8a38bc, 817a8ca3, c29df868, d215d00f), then `go test ./internal/xds/balancer/clusterimpl -run TestVerifyC3_ -v -count=1`
package clusterimpl

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type c3RecordingProvider struct {
	closed bool
}

func (p *c3RecordingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *c3RecordingProvider) Close() { p.closed = true }

// Root-provider construction succeeds, identity-provider construction fails.
// Observe whether the root provider is closed (or handed to a published owner)
// when handleSecurityConfig returns the error.
func TestVerifyC3_RootProviderLeakOnIdentityBuildFailure(t *testing.T) {
	root := &c3RecordingProvider{}
	origBuild := buildProvider
	defer func() { buildProvider = origBuild }()
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, _ string, wantIdentity, wantRoot bool) (certprovider.Provider, error) {
		if wantRoot {
			t.Logf("buildProvider(root instance %q) -> success", instanceName)
			return root, nil
		}
		t.Logf("buildProvider(identity instance %q) -> forced error", instanceName)
		return nil, errors.New("forced identity provider build failure")
	}

	xdsC := fakeclient.NewClient()
	xdsC.SetBootstrapConfig(&bootstrap.Config{})
	b := &clusterImplBalancer{xdsCredsInUse: true, xdsClient: xdsC}

	err := b.handleSecurityConfig(&xdsresource.SecurityConfig{
		RootInstanceName:     "root-instance",
		RootCertName:         "root",
		IdentityInstanceName: "identity-instance",
		IdentityCertName:     "identity",
	})
	t.Logf("handleSecurityConfig returned err=%v", err)
	t.Logf("root.closed=%v publishedHandshakeInfo=%v", root.closed, b.xdsHIPtr.Load() != nil)
	if err == nil {
		t.Fatal("expected error from identity provider build")
	}
	if !root.closed && b.xdsHIPtr.Load() == nil {
		t.Fatal("CONFIRMED: root provider neither closed nor transferred to a published owner on identity build failure")
	}
}
