// Run (on branch evalon/grpc-go-xd-7c3674ad): cp verify/repro/c1_overlap_ordering_test.go internal/xds/balancer/clusterimpl/tests/ && go test -tags verify_c1 ./internal/xds/balancer/clusterimpl/tests -run '^Test/VerifyC1_' -count=1 -v
//
//go:build verify_c1

// Repro for C1: the branch test TestSecurityConfigUpdate_WhileHandshakeLoadingRoots
// releases the blocked validation-root load (close(initialEvents.unblock)) as soon
// as replacementEvents.built fires. `built` fires from *inside* the replacement
// provider's Build function, i.e. before handleSecurityConfig has returned and
// before the replacement HandshakeInfo is published. These tests show that this
// synchronization permits the blocked load to resume, and the whole handshake to
// finish, before the replacement configuration is applied.
package clusterimpl_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/testdata"
	"net"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// ---- A provider builder identical in spirit to the branch's
// blockingCertProviderBuilder, with two extra observation points:
//   - buildReturned fires when the Build function is about to return (this is
//     strictly before handleSecurityConfig publishes the new HandshakeInfo).
//   - buildGate, when non-nil, blocks the Build function after `built` fires
//     until the test closes it (perturbation used by the deterministic test).

const verifyC1ProviderName = "verify-c1-cert-provider"

type verifyC1ProviderConfig struct {
	Label    string `json:"label"`
	RootFile string `json:"root_file"`
	Block    bool   `json:"block"`
}

type verifyC1Events struct {
	built         *grpcsync.Event
	buildReturned *grpcsync.Event
	loadStarted   *grpcsync.Event
	closed        *grpcsync.Event
	unblock       chan struct{}
	buildGate     chan struct{} // nil => Build does not block
	releaseGate   func()
}

var (
	verifyC1EventsMu  sync.Mutex
	verifyC1EventsMap = map[string]*verifyC1Events{}
)

func registerVerifyC1Events(t *testing.T, label string, gateBuild bool) *verifyC1Events {
	t.Helper()
	ev := &verifyC1Events{
		built:         grpcsync.NewEvent(),
		buildReturned: grpcsync.NewEvent(),
		loadStarted:   grpcsync.NewEvent(),
		closed:        grpcsync.NewEvent(),
		unblock:       make(chan struct{}),
	}
	if gateBuild {
		ev.buildGate = make(chan struct{})
	}
	verifyC1EventsMu.Lock()
	verifyC1EventsMap[label] = ev
	verifyC1EventsMu.Unlock()
	t.Cleanup(func() {
		verifyC1EventsMu.Lock()
		delete(verifyC1EventsMap, label)
		verifyC1EventsMu.Unlock()
	})
	return ev
}

func lookupVerifyC1Events(label string) *verifyC1Events {
	verifyC1EventsMu.Lock()
	defer verifyC1EventsMu.Unlock()
	return verifyC1EventsMap[label]
}

type verifyC1ProviderBuilder struct{}

func (verifyC1ProviderBuilder) Name() string { return verifyC1ProviderName }

func (verifyC1ProviderBuilder) ParseConfig(cfg any) (*certprovider.BuildableConfig, error) {
	raw, ok := cfg.(json.RawMessage)
	if !ok {
		return nil, fmt.Errorf("%s: unsupported config type: %T", verifyC1ProviderName, cfg)
	}
	var pc verifyC1ProviderConfig
	if err := json.Unmarshal(raw, &pc); err != nil {
		return nil, err
	}
	return certprovider.NewBuildableConfig(verifyC1ProviderName, raw, func(certprovider.BuildOptions) certprovider.Provider {
		pemData, err := os.ReadFile(pc.RootFile)
		if err != nil {
			return nil
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pemData) {
			return nil
		}
		ev := lookupVerifyC1Events(pc.Label)
		if ev != nil {
			ev.built.Fire() // <- this is the event the branch test awaits.
			if ev.buildGate != nil {
				<-ev.buildGate
			}
			defer ev.buildReturned.Fire()
		}
		return &verifyC1Provider{km: &certprovider.KeyMaterial{Roots: roots}, block: pc.Block, events: ev, closed: grpcsync.NewEvent()}
	}), nil
}

type verifyC1Provider struct {
	km     *certprovider.KeyMaterial
	block  bool
	events *verifyC1Events
	closed *grpcsync.Event
}

func (p *verifyC1Provider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	if p.events != nil {
		p.events.loadStarted.Fire()
		if p.block {
			select {
			case <-p.events.unblock:
			case <-p.closed.Done():
				return nil, fmt.Errorf("verify-c1: provider closed while loading")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if p.closed.HasFired() {
		return nil, fmt.Errorf("verify-c1: provider closed")
	}
	return p.km, nil
}

func (p *verifyC1Provider) Close() {
	p.closed.Fire()
	if p.events != nil {
		p.events.closed.Fire()
	}
}

func init() { certprovider.Register(verifyC1ProviderBuilder{}) }

func verifyC1BootstrapProvider(label, rootFile string, block bool) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"plugin_name": %q, "config": {"label": %q, "root_file": %q, "block": %t}}`, verifyC1ProviderName, label, rootFile, block))
}

// ---- Resolver wrapper: fires `applied` once the ClientConn has finished
// processing a resolver state whose Cluster security config uses the
// replacement root instance (same technique as the eval fixture's
// "Resolver post-UpdateState callback"). ccResolverWrapper.UpdateState blocks
// on the balancer tree's UpdateClientConnState, so this is a post-apply signal.

type verifyC1ResolverBuilder struct {
	resolver.Builder
	replacementRoot string
	applied         *grpcsync.Event
}

func (b *verifyC1ResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	return b.Builder.Build(target, &verifyC1ClientConn{ClientConn: cc, replacementRoot: b.replacementRoot, applied: b.applied}, opts)
}

type verifyC1ClientConn struct {
	resolver.ClientConn
	replacementRoot string
	applied         *grpcsync.Event
}

func (cc *verifyC1ClientConn) UpdateState(state resolver.State) error {
	err := cc.ClientConn.UpdateState(state)
	if err != nil || state.Attributes == nil {
		return err
	}
	config := xdsresource.XDSConfigFromResolverState(state)
	if config == nil {
		return nil
	}
	for _, result := range config.Clusters {
		if result == nil || result.Err != nil || result.Config.Cluster == nil || result.Config.Cluster.SecurityCfg == nil {
			continue
		}
		if result.Config.Cluster.SecurityCfg.RootInstanceName == cc.replacementRoot {
			cc.applied.Fire()
		}
	}
	return nil
}

// verifyC1ServerCreds wraps the server's TLS credentials and fires
// handshakeDone when a server-side TLS handshake completes successfully. On
// the client side this means the client validated the server certificate
// (against the roots it loaded) and finished the handshake.
type verifyC1ServerCreds struct {
	credentials.TransportCredentials
	handshakeDone *grpcsync.Event
}

func (c *verifyC1ServerCreds) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, ai, err := c.TransportCredentials.ServerHandshake(conn)
	if err == nil {
		c.handshakeDone.Fire()
	}
	return conn, ai, err
}

// verifyC1Setup mirrors the branch test's setup, but with the instrumented
// provider builder and the resolver wrapper above.
func verifyC1Setup(t *testing.T, gateReplacementBuild bool) (ctx context.Context, cancel context.CancelFunc, mgmt *e2e.ManagementServer, resources e2e.UpdateOptions, cc *grpc.ClientConn, initial, replacement *verifyC1Events, applied, serverHandshakeDone *grpcsync.Event, replacementInstance string) {
	t.Helper()
	mgmtServer := e2e.StartManagementServer(t, e2e.ManagementServerOptions{})
	const initialInstance = "verify-c1-initial-root"
	replacementInstance = "verify-c1-replacement-root"
	initial = registerVerifyC1Events(t, initialInstance, false)
	replacement = registerVerifyC1Events(t, replacementInstance, gateReplacementBuild)
	if gateReplacementBuild {
		// Never leave the balancer serializer stuck in Build on test exit.
		var once sync.Once
		t.Cleanup(func() { once.Do(func() { close(replacement.buildGate) }) })
		replacement.releaseGate = func() { once.Do(func() { close(replacement.buildGate) }) }
	}
	nodeID := uuid.New().String()
	bootstrapContents, err := bootstrap.NewContentsForTesting(bootstrap.ConfigOptionsForTesting{
		Servers:                            []byte(fmt.Sprintf(`[{"server_uri": %q, "channel_creds": [{"type": "insecure"}]}]`, mgmtServer.Address)),
		Node:                               []byte(fmt.Sprintf(`{"id": "%s"}`, nodeID)),
		ServerListenerResourceNameTemplate: e2e.ServerListenerResourceNameTemplate,
		CertificateProviders: map[string]json.RawMessage{
			initialInstance:     verifyC1BootstrapProvider(initialInstance, testdata.Path("x509/server_ca_cert.pem"), true),
			replacementInstance: verifyC1BootstrapProvider(replacementInstance, testdata.Path("x509/client_ca_cert.pem"), false),
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	r, err := internal.NewXDSResolverWithConfigForTesting.(func([]byte) (resolver.Builder, error))(bootstrapContents)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	applied = grpcsync.NewEvent()
	r = &verifyC1ResolverBuilder{Builder: r, replacementRoot: replacementInstance, applied: applied}
	cc, err = grpc.NewClient(r.Scheme()+":///test.service", grpc.WithTransportCredentials(xdsClientCredsWithInsecureFallback(t)), grpc.WithResolvers(r))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	cc.Connect()
	t.Cleanup(func() { cc.Close() })
	serverHandshakeDone = grpcsync.NewEvent()
	server := stubserver.StartTestService(t, nil, grpc.Creds(&verifyC1ServerCreds{TransportCredentials: tlsServerCreds(t), handshakeDone: serverHandshakeDone}))
	t.Cleanup(server.Stop)

	resources = e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: "test.service",
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       testutils.ParsePort(t, server.Address),
		SecLevel:   e2e.SecurityLevelNone,
	})
	resources.Clusters[0].TransportSocket = tlsTransportSocketWithRootInstance(t, initialInstance)
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	return ctx, cancel, mgmtServer, resources, cc, initial, replacement, applied, serverHandshakeDone, replacementInstance
}

// VerifyC1_BuiltDoesNotImplyApplied: deterministic demonstration. The
// replacement provider's Build function is held open after firing `built`.
// Following the branch test's exact synchronization (await built -> close
// unblock), the blocked handshake resumes and the TLS handshake completes
// (observed server-side) while the replacement Cluster security configuration
// has not been applied (Build has not returned, UpdateState has not completed,
// the initial provider is still owned by the balancer). Only after Build is
// allowed to return does the update apply and the initial provider close. Every
// assertion the branch test makes still holds in this ordering, so that test
// cannot distinguish it from a true overlap.
//
// (The RPC itself cannot complete while Build is held because the balancer
// serializer is busy in UpdateClientConnState; the handshake can.)
func (s) TestVerifyC1_BuiltDoesNotImplyApplied(t *testing.T) {
	ctx, cancel, mgmtServer, resources, cc, initial, replacement, applied, serverHandshakeDone, replacementInstance := verifyC1Setup(t, true)
	defer cancel()

	client := testgrpc.NewTestServiceClient(cc)
	pr := &peer.Peer{}
	rpcErrCh := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true), grpc.Peer(pr))
		rpcErrCh <- err
	}()
	select {
	case <-initial.loadStarted.Done():
	case <-ctx.Done():
		t.Fatal("timeout waiting for initial load to start")
	}

	// Same step as the branch test: push replacement, wait for `built`.
	resources.Clusters[0].TransportSocket = tlsTransportSocketWithRootInstance(t, replacementInstance)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	select {
	case <-replacement.built.Done():
	case <-ctx.Done():
		t.Fatal("timeout waiting for replacement provider to be built")
	}
	t.Logf("replacement.built fired; buildReturned=%v applied=%v", replacement.buildReturned.HasFired(), applied.HasFired())

	if serverHandshakeDone.HasFired() {
		t.Fatal("precondition failed: TLS handshake completed while the load was supposed to be blocked")
	}

	// Same step as the branch test: release the blocked load right after `built`.
	close(initial.unblock)
	select {
	case <-serverHandshakeDone.Done():
	case err := <-rpcErrCh:
		t.Fatalf("RPC finished (%v) before the handshake was observed", err)
	case <-ctx.Done():
		t.Fatal("timeout waiting for the TLS handshake to complete")
	}
	t.Logf("TLS handshake completed with initial roots; buildReturned=%v applied=%v initialClosed=%v",
		replacement.buildReturned.HasFired(), applied.HasFired(), initial.closed.HasFired())

	if replacement.buildReturned.HasFired() || applied.HasFired() {
		t.Fatalf("unexpected: replacement applied before we let Build return")
	}
	if initial.closed.HasFired() {
		t.Fatalf("unexpected: initial provider closed while balancer still owns it")
	}
	// Record the finding as a failure so it is visible in the test result:
	// the handshake resumed and finished before the replacement was applied.
	t.Errorf("FINDING: blocked root load resumed and the TLS handshake finished BEFORE the replacement security config was applied (built fired, Build not returned, UpdateState not completed)")

	// Now let the Build return; the update applies, the RPC completes over the
	// connection secured with the initial roots, and the initial provider closes.
	replacement.releaseGate()
	select {
	case <-applied.Done():
	case <-ctx.Done():
		t.Fatal("timeout waiting for replacement to be applied")
	}
	select {
	case err := <-rpcErrCh:
		if err != nil {
			t.Fatalf("EmptyCall() failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for RPC")
	}
	verifySecurityInformationFromPeer(t, pr, e2e.SecurityLevelMTLS)
	select {
	case <-initial.closed.Done():
	case <-ctx.Done():
		t.Fatal("timeout waiting for initial provider to close")
	}
	t.Logf("after releasing Build: applied=%v rpcOK=true initialClosed=%v", applied.HasFired(), initial.closed.HasFired())
}

// VerifyC1_NaturalOrdering: no perturbation. Runs the branch test's
// synchronization N times and records, at the instant close(unblock) is
// called, whether the replacement update had already been applied
// (post-UpdateState) and whether Build had even returned. Reports the counts.
func (s) TestVerifyC1_NaturalOrdering(t *testing.T) {
	const iterations = 25
	var unblockBeforeApplied, unblockBeforeBuildReturned atomic.Int32
	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iter%02d", i), func(t *testing.T) {
			ctx, cancel, mgmtServer, resources, cc, initial, replacement, applied, _, replacementInstance := verifyC1Setup(t, false)
			defer cancel()
			client := testgrpc.NewTestServiceClient(cc)
			rpcErrCh := make(chan error, 1)
			go func() {
				_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
				rpcErrCh <- err
			}()
			select {
			case <-initial.loadStarted.Done():
			case <-ctx.Done():
				t.Fatal("timeout waiting for initial load to start")
			}
			resources.Clusters[0].TransportSocket = tlsTransportSocketWithRootInstance(t, replacementInstance)
			if err := mgmtServer.Update(ctx, resources); err != nil {
				t.Fatal(err)
			}
			select {
			case <-replacement.built.Done():
			case <-ctx.Done():
				t.Fatal("timeout waiting for replacement provider to be built")
			}
			br := replacement.buildReturned.HasFired()
			ap := applied.HasFired()
			close(initial.unblock)
			if !ap {
				unblockBeforeApplied.Add(1)
			}
			if !br {
				unblockBeforeBuildReturned.Add(1)
			}
			t.Logf("at close(unblock): buildReturned=%v applied=%v", br, ap)
			select {
			case err := <-rpcErrCh:
				if err != nil {
					t.Fatalf("EmptyCall() failed: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("timeout waiting for RPC")
			}
			select {
			case <-initial.closed.Done():
			case <-time.After(defaultTestTimeout):
				t.Fatal("timeout waiting for initial provider to close")
			}
		})
	}
	t.Logf("SUMMARY: %d/%d iterations released the blocked load before the replacement update had been applied; %d/%d before the replacement Build function had even returned",
		unblockBeforeApplied.Load(), iterations, unblockBeforeBuildReturned.Load(), iterations)
}
