// Run: cp this file into internal/xds/server/ of branch evalon/grpc-go-xd-74048b3d, then `go test ./internal/xds/server -run '^TestC10ServerHandshakeInfoRetentionRefNeverReleased$' -v -count=1`
//
// Verification repro for C10 on branch evalon/grpc-go-xd-74048b3d.
// NewHandshakeInfo hands every HandshakeInfo an initial retention reference
// whose release closes the providers. The server path (connWrapper /
// ServerHandshake) never calls Release, so that reference is never dropped and
// the HandshakeInfo still admits Acquire after connWrapper.Close closed its
// providers directly.

package server

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/clients/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

const c10PluginName = "c10_recording_cert_provider"

var c10Closes atomic.Int32

type c10Provider struct{}

func (c10Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return nil, errors.New("no key material")
}

func (c10Provider) Close() { c10Closes.Add(1) }

type c10Builder struct{}

func (c10Builder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
	return certprovider.NewBuildableConfig(c10PluginName, nil, func(certprovider.BuildOptions) certprovider.Provider {
		return c10Provider{}
	}), nil
}

func (c10Builder) Name() string { return c10PluginName }

type c10XDSClient struct{ bc *bootstrap.Config }

func (c *c10XDSClient) WatchResource(string, string, xdsclient.ResourceWatcher) func() {
	return func() {}
}

func (c *c10XDSClient) BootstrapConfig() *bootstrap.Config { return c.bc }

func TestC10ServerHandshakeInfoRetentionRefNeverReleased(t *testing.T) {
	certprovider.Register(c10Builder{})
	bc, err := bootstrap.NewConfigFromContents([]byte(`{
		"xds_servers": [{"server_uri": "ipv4:///127.0.0.1:443", "channel_creds": [{"type": "insecure"}]}],
		"certificate_providers": {"c10": {"plugin_name": "` + c10PluginName + `"}}
	}`))
	if err != nil {
		t.Fatalf("bootstrap.NewConfigFromContents() failed: %v", err)
	}

	client, server := net.Pipe()
	defer client.Close()
	lw := &listenerWrapper{xdsC: &c10XDSClient{bc: bc}, conns: make(map[*connWrapper]bool)}
	cw := &connWrapper{
		Conn:   server,
		parent: lw,
		filterChain: &filterChain{securityCfg: &xdsresource.SecurityConfig{
			IdentityInstanceName: "c10",
			RootInstanceName:     "c10",
			RequireClientCert:    true,
		}},
	}
	lw.conns[cw] = true

	hi, err := cw.XDSHandshakeInfo()
	if err != nil {
		t.Fatalf("XDSHandshakeInfo() failed: %v", err)
	}
	t.Logf("after XDSHandshakeInfo(): provider Close calls=%d", c10Closes.Load())

	// The server lifecycle ends with connWrapper.Close, which closes the
	// providers directly and never touches hi's reference count.
	cw.Close()
	closesAfterConnClose := c10Closes.Load()
	t.Logf("after connWrapper.Close(): provider Close calls=%d", closesAfterConnClose)

	acq := hi.Acquire()
	t.Logf("hi.Acquire() after connWrapper.Close() -> %v (retention ref still outstanding)", acq)
	if acq {
		hi.Release()
	}

	// Dropping the retention reference ourselves is what the server never does;
	// only then does the HandshakeInfo's own onZero run (a second Close on the
	// already-closed providers, absorbed by singleCloseWrappedProvider).
	hi.Release()
	t.Logf("after manually releasing the initial ref: provider Close calls=%d (onZero ran only now; its second Close is absorbed by singleCloseWrappedProvider)", c10Closes.Load())
	if acq2 := hi.Acquire(); acq2 {
		t.Fatalf("hi.Acquire() after final release -> %v, want false", acq2)
	}
	t.Logf("hi.Acquire() after final release -> false")

	if !acq {
		t.Fatalf("REFUTED: HandshakeInfo did not admit Acquire after connWrapper.Close()")
	}
	t.Logf("CONFIRMED: server HandshakeInfo keeps its initial retention reference after connWrapper.Close(); refcount does not reflect provider liveness")
}
