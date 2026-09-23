// Audit instrumentation for claim C1 (run ID v-618a6113). This file is build-ignored here; to run it:
//   cp verify/repro/c1_followup_roots_test.go <worktree of claims/evalon/grpc-go-xd-e2cd665a>/internal/xds/balancer/clusterimpl/tests/verify_c1_followup_test.go
//   sed -i '/^\/\/go:build ignore$/d' .../verify_c1_followup_test.go
//   go test ./internal/xds/balancer/clusterimpl/tests/ -run '^Test$/^VerifyC1_' -count=1 -v
//
// It replays the follow-up-connection section of
// TestSecurityConfigUpdate_ReplacedDuringHandshake in three variants and
// reports what the follow-up attempt actually observes, so the
// distinguishability of the solution's assertions ("x509" substring,
// TransientFailure, untrustedRoot.loadStarted) can be judged empirically.

//go:build ignore

package clusterimpl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

const (
	vTrustedRootInstance   = "v-trusted-root-certificate-provider-instance"
	vTrusted2RootInstance  = "v-trusted2-root-certificate-provider-instance"
	vUntrustedRootInstance = "v-untrusted-root-certificate-provider-instance"
)

// verifyC1Setup runs the solution's test up to and including the completion
// of the first (blocked, then unblocked) handshake, optionally replacing the
// cluster security config with `replacement` while the handshake is blocked.
// It returns the client conn, the resources, the replacement provider (nil if
// no replacement), and the trusted provider.
func verifyC1Setup(t *testing.T, ctx context.Context, replacement string) (*grpc.ClientConn, e2e.UpdateOptions, *e2e.ManagementServer, *blockingRootProvider, *blockingRootProvider) {
	mgmtServer := e2e.StartManagementServer(t, e2e.ManagementServerOptions{})
	nodeID := uuid.New().String()
	bc, err := bootstrap.NewContentsForTesting(bootstrap.ConfigOptionsForTesting{
		Servers: []byte(fmt.Sprintf(`[{
			"server_uri": "passthrough:///%s",
			"channel_creds": [{"type": "insecure"}]
		}]`, mgmtServer.Address)),
		Node: []byte(fmt.Sprintf(`{"id": "%s"}`, nodeID)),
		CertificateProviders: map[string]json.RawMessage{
			vTrustedRootInstance: json.RawMessage(fmt.Sprintf(`{
				"plugin_name": "%s",
				"config": {"root_file": "x509/server_ca_cert.pem", "block_loads": true}
			}`, blockingRootProviderPluginName)),
			vTrusted2RootInstance: json.RawMessage(fmt.Sprintf(`{
				"plugin_name": "%s",
				"config": {"root_file": "x509/server_ca_cert.pem"}
			}`, blockingRootProviderPluginName)),
			vUntrustedRootInstance: json.RawMessage(fmt.Sprintf(`{
				"plugin_name": "%s",
				"config": {"root_file": "x509/client_ca_cert.pem"}
			}`, blockingRootProviderPluginName)),
		},
	})
	if err != nil {
		t.Fatalf("Failed to create bootstrap configuration: %v", err)
	}
	cc, serverAddress := setupForSecurityTests(t, bc, xdsClientCredsWithInsecureFallback(t), tlsServerCreds(t))
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: "test.service",
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       testutils.ParsePort(t, serverAddress),
		SecLevel:   e2e.SecurityLevelNone,
	})
	resources.Clusters[0].TransportSocket = tlsTransportSocketWithRootInstance(t, vTrustedRootInstance)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	builtCh := blockingRootProviderBuilderInstance.builtCh
	v, err := builtCh.Receive(ctx)
	if err != nil {
		t.Fatalf("Timed out waiting for the trusted root provider to be built: %v", err)
	}
	trustedRoot := v.(*blockingRootProvider)
	select {
	case <-trustedRoot.loadStarted:
	case <-ctx.Done():
		t.Fatal("Timed out waiting for the handshake to load trusted roots")
	}
	client := testgrpc.NewTestServiceClient(cc)
	peer := &peer.Peer{}
	rpcErrCh := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true), grpc.Peer(peer))
		rpcErrCh <- err
	}()

	var replacementRoot *blockingRootProvider
	if replacement != "" {
		resources.Clusters[0].TransportSocket = tlsTransportSocketWithRootInstance(t, replacement)
		if err := mgmtServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		v, err = builtCh.Receive(ctx)
		if err != nil {
			t.Fatalf("Timed out waiting for the replacement root provider to be built: %v", err)
		}
		replacementRoot = v.(*blockingRootProvider)
	}
	sCtx, sCancel := context.WithTimeout(ctx, defaultTestShortTimeout)
	defer sCancel()
	select {
	case <-trustedRoot.closed:
		t.Fatal("Trusted root provider closed while a handshake using it is in progress")
	case err := <-rpcErrCh:
		t.Fatalf("EmptyCall() returned %v while the handshake was blocked loading trusted roots", err)
	case <-sCtx.Done():
	}
	close(trustedRoot.unblockLoads)
	select {
	case err := <-rpcErrCh:
		if err != nil {
			t.Fatalf("EmptyCall() failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timed out waiting for EmptyCall() to complete")
	}
	verifySecurityInformationFromPeer(t, peer, e2e.SecurityLevelMTLS)
	if replacement != "" {
		select {
		case <-trustedRoot.closed:
		case <-ctx.Done():
			t.Fatal("Timed out waiting for the replaced root provider to be closed")
		}
	}
	return cc, resources, mgmtServer, trustedRoot, replacementRoot
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// followUp points the cluster at a fresh server (same certificate) and
// returns the resulting RPC error and channel state.
func followUp(t *testing.T, ctx context.Context, cc *grpc.ClientConn, resources e2e.UpdateOptions, mgmtServer *e2e.ManagementServer, wantState connectivity.State) error {
	server2 := stubserver.StartTestService(t, nil, grpc.Creds(tlsServerCreds(t)))
	t.Cleanup(server2.Stop)
	resources.Endpoints[0] = e2e.DefaultEndpoint(resources.Endpoints[0].ClusterName, "localhost", []uint32{testutils.ParsePort(t, server2.Address)})
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	testutils.AwaitState(ctx, t, cc, wantState)
	client := testgrpc.NewTestServiceClient(cc)
	if wantState != connectivity.Ready {
		_, err := client.EmptyCall(ctx, &testpb.Empty{})
		return err
	}
	// The channel may still be READY on the old connection; keep issuing RPCs
	// until one is served by server2, proving a new connection was made.
	for {
		p := &peer.Peer{}
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(p))
		if err != nil {
			return err
		}
		if p.Addr != nil && p.Addr.String() == server2.Address {
			t.Logf("OBS follow-up RPC served by server2 %s", p.Addr)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// Variant 1: the solution's scenario (replacement = untrusted roots), with
// extra observations: freshness of untrustedRoot.loadStarted immediately
// before the follow-up trigger, and the exact follow-up error string.
func (s) TestVerifyC1_ReplacementUntrusted_FollowupObservations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cc, resources, mgmtServer, trustedRoot, untrustedRoot := verifyC1Setup(t, ctx, vUntrustedRootInstance)

	t.Logf("OBS pre-trigger: untrustedRoot.loadStarted closed=%v, untrustedRoot.closed=%v, trustedRoot.closed=%v",
		isClosed(untrustedRoot.loadStarted), isClosed(untrustedRoot.closed), isClosed(trustedRoot.closed))
	preTriggerLoaded := isClosed(untrustedRoot.loadStarted)

	err := followUp(t, ctx, cc, resources, mgmtServer, connectivity.TransientFailure)
	t.Logf("OBS follow-up RPC error: code=%v msg=%q", status.Code(err), err)
	t.Logf("OBS post-trigger: untrustedRoot.loadStarted closed=%v (was closed before trigger: %v)", isClosed(untrustedRoot.loadStarted), preTriggerLoaded)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("follow-up RPC code = %v, want Unavailable", status.Code(err))
	}
}

// Variant 2: no replacement at all -- the follow-up connection is governed by
// the prior (trusted) roots. Shows what the follow-up assertions of the
// solution's test would observe if the prior roots still governed.
func (s) TestVerifyC1_NoReplacement_PriorRootsGovernFollowup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cc, resources, mgmtServer, trustedRoot, _ := verifyC1Setup(t, ctx, "")

	err := followUp(t, ctx, cc, resources, mgmtServer, connectivity.Ready)
	t.Logf("OBS follow-up RPC error under prior roots: %v (trustedRoot.closed=%v)", err, isClosed(trustedRoot.closed))
	if err != nil {
		t.Fatalf("follow-up RPC under prior roots failed: %v", err)
	}
}

// Variant 3: replacement with a provider carrying the *same* trusted roots
// (different instance). The follow-up connection is governed by the
// replacement, but the replacement trusts the server CA, so no trust failure
// must be observed.
func (s) TestVerifyC1_ReplacementTrusted_FollowupSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cc, resources, mgmtServer, _, trusted2 := verifyC1Setup(t, ctx, vTrusted2RootInstance)

	t.Logf("OBS pre-trigger: trusted2.loadStarted closed=%v", isClosed(trusted2.loadStarted))
	err := followUp(t, ctx, cc, resources, mgmtServer, connectivity.Ready)
	t.Logf("OBS follow-up RPC error under trusted replacement: %v; trusted2.loadStarted closed=%v", err, isClosed(trusted2.loadStarted))
	if err != nil {
		t.Fatalf("follow-up RPC under trusted replacement failed: %v", err)
	}
}
