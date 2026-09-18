//go:build ignore

// Run (branch evalon/grpc-go-xd-1e9d8011): sed '/^\/\/go:build ignore$/d' verify/repro/c9_double_close_during_load_test.go > internal/credentials/xds/audit_c9_test.go && go test ./internal/credentials/xds/ -run 'Test/AuditC9' -count=1 -v
//
// Audit repro for claim C9: while a client handshake holds a validation-root
// load reference (KeyMaterial blocked), calling HandshakeInfo.Close twice
// closes the root provider out from under the active load instead of
// deferring closure to the load's release.

package xds

import (
	"context"
	"crypto/x509"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

type auditC9Provider struct {
	loadStarted chan struct{}
	unblock     chan struct{}
	closed      chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
}

func (p *auditC9Provider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	p.startOnce.Do(func() { close(p.loadStarted) })
	select {
	case <-p.unblock:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}
func (p *auditC9Provider) Close() { p.closeOnce.Do(func() { close(p.closed) }) }
func (p *auditC9Provider) isClosed() bool {
	select {
	case <-p.closed:
		return true
	default:
		return false
	}
}

func (s) TestAuditC9_DoubleCloseDuringRootLoad(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	root := &auditC9Provider{loadStarted: make(chan struct{}), unblock: make(chan struct{}), closed: make(chan struct{})}
	hi := NewHandshakeInfo(root, nil, nil, false, "", false, false)

	cfgErr := make(chan error, 1)
	go func() {
		_, err := hi.ClientSideTLSConfig(ctx, "")
		cfgErr <- err
	}()
	select {
	case <-root.loadStarted:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for the root load to start")
	}
	fmt.Printf("AUDIT root load in progress; root provider closed=%v\n", root.isClosed())

	hi.Close()
	fmt.Printf("AUDIT after Close() #1 (owner release): root provider closed=%v\n", root.isClosed())
	hi.Close()
	closedAfterSecond := root.isClosed()
	fmt.Printf("AUDIT after Close() #2 (redundant): root provider closed=%v\n", closedAfterSecond)

	close(root.unblock)
	select {
	case err := <-cfgErr:
		fmt.Printf("AUDIT ClientSideTLSConfig returned err=%v\n", err)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for ClientSideTLSConfig to return")
	}
	fmt.Printf("AUDIT after load release: root provider closed=%v\n", root.isClosed())

	if closedAfterSecond {
		t.Log("RESULT: second Close() closed the root provider while a load still held a reference (claim C9 confirmed)")
		return
	}
	t.Log("RESULT: root provider stayed open across the double Close until the active load released it (claim C9 refuted)")
}
