// Run: bash verify/repro/c7/run.sh   (same instrumentation as c7_probe_test.go; this file is copied into internal/xds/server and exercises the server-side production path)
//go:build verifyrepro

package server

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
)

const c7PluginName = "c7-exposure-probe-plugin"

type c7Inner struct {
	entered chan struct{}
	release chan struct{}
	closed  atomic.Bool
}

func (p *c7Inner) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.closed.Load() {
		return nil, errors.New("underlying provider was closed while this KeyMaterial call was in progress")
	}
	return &certprovider.KeyMaterial{}, nil
}
func (p *c7Inner) Close() { p.closed.Store(true) }

type c7Builder struct{ inner *c7Inner }

func (b *c7Builder) Name() string { return c7PluginName }
func (b *c7Builder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
	return certprovider.NewBuildableConfig(c7PluginName, nil, func(certprovider.BuildOptions) certprovider.Provider { return b.inner }), nil
}

// TestC7Probe_ServerHandshakeExposure mirrors the production server path: the
// connWrapper owns store-built providers and closes them in connWrapper.Close
// with no HandshakeInfo ownership. A server handshake's root load
// (HandshakeInfo.ServerSideTLSConfig -> wrapper.KeyMaterial) is admitted in the
// interval after Close observed zero loads, and the underlying provider is then
// closed underneath it.
func (s) TestC7Probe_ServerHandshakeExposure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inner := &c7Inner{entered: make(chan struct{}, 1), release: make(chan struct{})}
	certprovider.Register(&c7Builder{inner: inner})
	// The server side requires an identity provider; the same store-built wrapper
	// is used for both roles here, exactly as connWrapper.XDSHandshakeInfo does
	// when the identity and root instances coincide.
	ip, err := certprovider.GetProvider(c7PluginName, nil, certprovider.BuildOptions{CertName: "c7", WantIdentity: true, WantRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	defer c2.Close()
	cw := &connWrapper{Conn: c1, identityProvider: ip, rootProvider: ip, parent: &listenerWrapper{conns: map[*connWrapper]bool{}}}
	hi := xdsinternal.NewHandshakeInfo(cw.rootProvider, cw.identityProvider, nil, false, "", false, false)

	type res struct {
		err error
	}
	resCh := make(chan res, 1)
	certprovider.C7TestHookBeforeCloseProvider = func() {
		go func() {
			_, err := hi.ServerSideTLSConfig(ctx)
			resCh <- res{err}
		}()
		select {
		case <-inner.entered:
		case <-ctx.Done():
			t.Error("timed out waiting for the server handshake to enter KeyMaterial")
		}
	}
	defer func() { certprovider.C7TestHookBeforeCloseProvider = nil }()

	cw.Close()
	closedDuringLoad := inner.closed.Load()
	t.Logf("C7PROBE after connWrapper.Close() returned: underlying provider closed=%v while the server handshake's KeyMaterial was in progress", closedDuringLoad)
	if !closedDuringLoad {
		// Wait for the deferred close (if any) to happen once the load ends.
		defer func() { t.Logf("C7PROBE after load finished: underlying closed=%v", inner.closed.Load()) }()
	}
	close(inner.release)
	r := <-resCh
	t.Logf("C7PROBE server handshake ServerSideTLSConfig err=%v", r.err)
	if closedDuringLoad || r.err != nil {
		t.Errorf("C7PROBE RESULT: production server handshake reached the admission-vs-close interval unprotected; provider closed underneath its load")
	} else {
		t.Logf("C7PROBE RESULT: server handshake load completed before the provider was closed")
	}
}
