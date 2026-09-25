// Run (C4, branch evalon/grpc-go-xd-e76d07e7): cp verify/repro/c4_acquire_retry_limit_test.go <e76d07e7-worktree>/internal/xds/balancer/clusterimpl/ && cd <e76d07e7-worktree> && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC4' -count=1
package clusterimpl

import (
	"context"
	"crypto/x509"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	xdscreds "google.golang.org/grpc/credentials/xds"
	icredentials "google.golang.org/grpc/internal/credentials"
	"google.golang.org/grpc/internal/credentials/xds"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/resolver"
)

type c4Provider struct{}

func (c4Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}
func (c4Provider) Close() {}

func c4State(xdsC *fakeclient.Client, rootCertName string) balancer.ClientConnState {
	state := xdsclient.SetClient(resolver.State{Endpoints: testBackendEndpoints}, xdsC)
	state = xdsresource.SetXDSConfig(state, &xdsresource.XDSConfig{
		Clusters: map[string]*xdsresource.ClusterResult{
			testClusterName: {
				Config: xdsresource.ClusterConfig{
					Cluster: &xdsresource.ClusterUpdate{
						ClusterName:    testClusterName,
						ClusterType:    xdsresource.ClusterTypeEDS,
						EDSServiceName: testServiceName,
						SecurityCfg:    &xdsresource.SecurityConfig{RootInstanceName: "root-instance", RootCertName: rootCertName},
					},
					EndpointConfig: &xdsresource.EndpointConfig{EDSUpdate: &xdsresource.EndpointsUpdate{}},
				},
			},
		},
	})
	return balancer.ClientConnState{
		ResolverState:  state,
		BalancerConfig: &LBConfig{Cluster: testClusterName, ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}},
	}
}

// Drives successive Cluster security replacements through the production
// clusterimpl balancer (UpdateClientConnState -> handleSecurityConfig ->
// publishHandshakeInfo) while real xDS ClientHandshake calls run against the
// HandshakeInfo pointer the balancer hands to SubConns. Fails if any
// ClientHandshake returns the acquisition error although a usable
// configuration is published.
func (s) TestVerifyC4_ClientHandshakeAbortsWhileUsableConfigPublished(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	origBuildProvider := buildProvider
	buildProvider = func(map[string]*certprovider.BuildableConfig, string, string, bool, bool) (certprovider.Provider, error) {
		return c4Provider{}, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	creds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: creds})
	defer b.Close()
	xdsC := fakeclient.NewClient()
	xdsC.SetBootstrapConfig(&bootstrap.Config{})
	if err := b.UpdateClientConnState(c4State(xdsC, "root-0")); err != nil {
		t.Fatal(err)
	}
	var addrs []resolver.Address
	select {
	case addrs = <-cc.NewSubConnAddrsCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for NewSubConn")
	}
	hiPtr := xds.HandshakeInfoFromAttributes(addrs[0].Attributes)
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addrs[0].Attributes})

	var stop atomic.Bool
	var updates, handshakes, acqErrs atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // Sole caller of balancer APIs: successive replacements A -> B -> C -> ...
		defer wg.Done()
		for i := 1; !stop.Load(); i++ {
			if err := b.UpdateClientConnState(c4State(xdsC, []string{"root-1", "root-2"}[i%2])); err != nil {
				t.Errorf("UpdateClientConnState: %v", err)
				return
			}
			updates.Add(1)
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				c1, c2 := net.Pipe()
				c2.Close()
				_, _, err := creds.ClientHandshake(hsCtx, "authority", c1)
				c1.Close()
				handshakes.Add(1)
				if err != nil && strings.Contains(err.Error(), "no longer in use") {
					acqErrs.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				}
			}
		}()
	}
	runFor := 15 * time.Second
	if secs, err := strconv.Atoi(os.Getenv("VERIFY_C4_SECONDS")); err == nil && secs > 0 {
		runFor = time.Duration(secs) * time.Second
	}
	deadline := time.Now().Add(runFor)
	for time.Now().Before(deadline) && acqErrs.Load() == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	published := hiPtr.Load()
	acquirable := published.Acquire()
	if acquirable {
		published.Release()
	}
	t.Logf("security replacements=%d handshakes=%d acquisition-errors=%d; published config acquirable after run=%v", updates.Load(), handshakes.Load(), acqErrs.Load(), acquirable)
	if acqErrs.Load() > 0 {
		t.Errorf("ClientHandshake aborted %d times with %q although the balancer always publishes a live replacement before retiring the old one", acqErrs.Load(), firstErr.Load())
	}
}

// Same ClientHandshake and HandshakeInfo code, with the replacement schedule
// driven directly with publishHandshakeInfo's exact operations
// (old := hiPtr.Swap(new); old.Retire()) so replacements are frequent enough
// to hit the Load/Acquire windows. Fails if ClientHandshake returns the
// acquisition error while a usable configuration is published.
func (s) TestVerifyC4_SwapRetireScheduleDirect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	creds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	var hiPtr atomic.Pointer[xds.HandshakeInfo]
	hiPtr.Store(xds.NewHandshakeInfo(c4Provider{}, nil, nil, false, "", false, false))
	addr := xds.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	var stop atomic.Bool
	var updates, handshakes, acqErrs, publishedUsable atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if old := hiPtr.Swap(xds.NewHandshakeInfo(c4Provider{}, nil, nil, false, "", false, false)); old != nil {
				old.Retire()
			}
			updates.Add(1)
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				c1, c2 := net.Pipe()
				c2.Close()
				_, _, err := creds.ClientHandshake(hsCtx, "authority", c1)
				c1.Close()
				handshakes.Add(1)
				if err != nil && strings.Contains(err.Error(), "no longer in use") {
					acqErrs.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					// Right after the failure a replacement is published and acquirable.
					if cur := hiPtr.Load(); cur != nil && cur.Acquire() {
						publishedUsable.Add(1)
						cur.Release()
					}
				}
			}
		}()
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && acqErrs.Load() < 5 {
		time.Sleep(20 * time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()
	t.Logf("swap+retire replacements=%d handshakes=%d acquisition-errors=%d; errors immediately followed by an acquirable published config=%d", updates.Load(), handshakes.Load(), acqErrs.Load(), publishedUsable.Load())
	if acqErrs.Load() > 0 {
		t.Errorf("ClientHandshake aborted %d times with %q while a usable configuration was published", acqErrs.Load(), firstErr.Load())
	}
}
