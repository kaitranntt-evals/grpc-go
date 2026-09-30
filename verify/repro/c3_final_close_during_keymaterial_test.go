// Run: verify/repro/c3_final_close_during_keymaterial.sh   (copies this file into a worktree of evalon/grpc-go-xd-03538fd6 and runs: go test -tags verify_repro ./.evaltools/c3 -run TestC3 -count=1 -v)

//go:build verify_repro

// C3 repro: the production wrapper returned by certprovider.BuildableConfig.Build
// (*certprovider.singleCloseWrappedProvider, which exposes both KeyMaterial and Close) admits a
// KeyMaterial call; while that call is still blocked inside the underlying provider, the final (only)
// owner calls Close on the wrapper. The test PASSES when the problem is present: the underlying
// provider's Close runs before the admitted KeyMaterial call returns, and that call then fails.
package c3repro

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

type underlying struct {
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (p *underlying) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	close(p.entered)
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-p.closed:
		return nil, errors.New("underlying provider was closed during the admitted load")
	default:
	}
	return &certprovider.KeyMaterial{}, nil
}

func (p *underlying) Close() { close(p.closed) }

// distributorBacked is an underlying provider built on the production certprovider.Distributor, the building
// block of the production pemfile (file_watcher) provider: KeyMaterial blocks until material is available,
// Close stops the distributor.
type distributorBacked struct {
	*certprovider.Distributor
	entered chan struct{}
}

func (p *distributorBacked) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	close(p.entered)
	return p.Distributor.KeyMaterial(ctx)
}

func (p *distributorBacked) Close() { p.Distributor.Stop() }

// Same interleaving with a Distributor-backed underlying provider: the admitted call is failed by the final
// owner's Close ("provider instance is closed") instead of being allowed to finish.
func TestC3FinalOwnerCloseFailsAdmittedDistributorLoad(t *testing.T) {
	u := &distributorBacked{Distributor: certprovider.NewDistributor(), entered: make(chan struct{})}
	bc := certprovider.NewBuildableConfig("c3-repro-distributor", []byte("cfg"), func(certprovider.BuildOptions) certprovider.Provider { return u })
	handle, err := bc.Build(certprovider.BuildOptions{CertName: "c3"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production wrapper under test: %T", handle)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { _, err := handle.KeyMaterial(ctx); errCh <- err }()
	<-u.entered
	time.Sleep(50 * time.Millisecond)       // let the admitted call park inside the Distributor
	handle.Close()                          // final owner releases the only handle
	u.Set(&certprovider.KeyMaterial{}, nil) // material arriving after the close can no longer help
	err = <-errCh
	t.Logf("OBSERVED: admitted KeyMaterial call returned err=%v", err)
	if err == nil {
		t.Fatal("NOT REPRODUCED: the admitted KeyMaterial call still succeeded")
	}
}

func TestC3FinalOwnerCloseDuringAdmittedKeyMaterial(t *testing.T) {
	u := &underlying{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	bc := certprovider.NewBuildableConfig("c3-repro", []byte("cfg"), func(certprovider.BuildOptions) certprovider.Provider { return u })
	handle, err := bc.Build(certprovider.BuildOptions{CertName: "c3"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production wrapper under test: %T", handle)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { _, err := handle.KeyMaterial(ctx); errCh <- err }()
	<-u.entered // The KeyMaterial call is admitted and running inside the underlying provider.

	handle.Close() // Final owner releases the only handle.

	select {
	case <-u.closed:
		t.Log("OBSERVED: underlying provider Close() ran while the admitted KeyMaterial call was still blocked")
	case err := <-errCh:
		t.Fatalf("KeyMaterial returned before underlying close: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("NOT REPRODUCED: underlying provider stayed open while the admitted KeyMaterial call was running")
	}
	select {
	case err := <-errCh:
		t.Fatalf("admitted KeyMaterial call returned before release: %v", err)
	default:
	}
	close(u.release)
	err = <-errCh
	t.Log(fmt.Sprintf("OBSERVED: admitted KeyMaterial call returned err=%v", err))
	if err == nil {
		t.Fatal("NOT REPRODUCED: the admitted KeyMaterial call still succeeded")
	}
}
