// Run (on branch b86f6a10): cp verify/repro/c5_server_owner_ref_unreleased_test.go internal/xds/server/zz_verify_c5_test.go && go test ./internal/xds/server -run 'TestVerifyC5_' -count=1 -v
//
// Part 1 shows that NewHandshakeInfo hands the caller an initial owner reference that keeps the
// providers open until an explicit Release. Part 2 drives the server-side lifecycle exactly as
// credentials/xds.ServerHandshake does (connWrapper.XDSHandshakeInfo, no Acquire/Release) followed
// by the existing teardown (connWrapper.Close closes the providers directly) and observes whether
// the HandshakeInfo's owner reference is still held afterwards.

package server

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type verifyC5Provider struct{ closed atomic.Int32 }

func (p *verifyC5Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *verifyC5Provider) Close() { p.closed.Add(1) }

var (
	verifyC5Mu    sync.Mutex
	verifyC5Built []*verifyC5Provider
)

type verifyC5Builder struct{}

func (verifyC5Builder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
	return certprovider.NewBuildableConfig("verify_c5_provider", nil, func(certprovider.BuildOptions) certprovider.Provider {
		p := &verifyC5Provider{}
		verifyC5Mu.Lock()
		verifyC5Built = append(verifyC5Built, p)
		verifyC5Mu.Unlock()
		return p
	}), nil
}
func (verifyC5Builder) Name() string { return "verify_c5_provider" }

func init() { certprovider.Register(verifyC5Builder{}) }

type verifyC5XDSClient struct {
	XDSClient
	bc *bootstrap.Config
}

func (c *verifyC5XDSClient) BootstrapConfig() *bootstrap.Config { return c.bc }

func TestVerifyC5_ConstructorOwnerReference(t *testing.T) {
	p := &verifyC5Provider{}
	hi := xdsinternal.NewHandshakeInfo(nil, p, nil, false, "", false, false)
	got := hi.Acquire()
	t.Logf("Acquire() right after NewHandshakeInfo: %v (true => constructor created a live owner reference)", got)
	hi.Release() // undo the Acquire above
	t.Logf("provider Close() calls with only the constructor's reference outstanding: %d", p.closed.Load())
	hi.Release() // the explicit owner Release
	t.Logf("provider Close() calls after the explicit owner Release(): %d", p.closed.Load())
	if !got || p.closed.Load() != 1 {
		t.Errorf("unexpected: acquire=%v closeCalls=%d", got, p.closed.Load())
	}
}

func TestVerifyC5_ServerLifecycleLeavesOwnerRef(t *testing.T) {
	bc, err := bootstrap.NewConfigFromContents([]byte(`{
		"xds_servers": [{"server_uri": "ipv4:///127.0.0.1:443", "channel_creds": [{"type": "insecure"}]}],
		"certificate_providers": {"verify-c5": {"plugin_name": "verify_c5_provider"}}
	}`))
	if err != nil {
		t.Fatalf("bootstrap.NewConfigFromContents() failed: %v", err)
	}
	lw := &listenerWrapper{xdsC: &verifyC5XDSClient{bc: bc}, conns: make(map[*connWrapper]bool)}
	c1, c2 := net.Pipe()
	defer c2.Close()
	cw := &connWrapper{
		Conn:   c1,
		parent: lw,
		filterChain: &filterChain{securityCfg: &xdsresource.SecurityConfig{
			IdentityInstanceName: "verify-c5",
			RootInstanceName:     "verify-c5",
			RequireClientCert:    true,
		}},
	}
	lw.conns[cw] = true

	verifyC5Mu.Lock()
	verifyC5Built = nil
	verifyC5Mu.Unlock()

	// This is what credentials/xds.ServerHandshake does: obtain the HandshakeInfo and use it,
	// without ever calling Acquire or Release on it.
	hi, err := cw.XDSHandshakeInfo()
	if err != nil {
		t.Fatalf("XDSHandshakeInfo() failed: %v", err)
	}
	verifyC5Mu.Lock()
	built := append([]*verifyC5Provider(nil), verifyC5Built...)
	verifyC5Mu.Unlock()
	t.Logf("providers built by XDSHandshakeInfo(): %d", len(built))

	// Existing teardown: connWrapper.Close() closes the providers directly.
	if err := cw.Close(); err != nil {
		t.Fatalf("connWrapper.Close() failed: %v", err)
	}
	for i, p := range built {
		t.Logf("after connWrapper.Close(): provider[%d].Close() calls = %d", i, p.closed.Load())
	}

	stillOwned := hi.Acquire()
	t.Logf("hi.Acquire() after teardown = %v (true => the initial owner reference from NewHandshakeInfo is still held)", stillOwned)
	if stillOwned {
		hi.Release() // undo the probe Acquire
		hi.Release() // release the leftover owner reference; HandshakeInfo now calls Close() on the (already closed) providers again
		for i, p := range built {
			t.Logf("after releasing the leftover owner reference: underlying provider[%d].Close() calls = %d (a second Close() on the certprovider store wrapper only decrements its refCount, so the underlying count does not change)", i, p.closed.Load())
		}
		t.Errorf("CONFIRMED: server-side lifecycle (XDSHandshakeInfo -> connWrapper.Close) left the HandshakeInfo owner reference unreleased after teardown")
	}
}
