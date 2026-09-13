// C2 repro: copy to internal/credentials/xds/c2_extra_release_test.go on branch
// evalon/grpc-go-xd-dcaf16f3 and run
//   go test ./internal/credentials/xds -run 'Test/VerifyC2ExtraReleaseGoesNegative$' -count=1 -v
// It performs one extra Release after the owner's final valid release and prints
// the resulting reference count; the test fails if the count went negative
// without a panic or a logged error.
// Remove the go:build ignore line below after copying the file into the target package.

//go:build ignore

package xds

import (
	"bytes"
	"context"
	"log"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/grpclog"
)

type c2CloseCounter struct {
	closes int
}

func (p *c2CloseCounter) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return nil, nil
}
func (p *c2CloseCounter) Close() { p.closes++ }

func (s) TestVerifyC2ExtraReleaseGoesNegative(t *testing.T) {
	var logBuf bytes.Buffer
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(&logBuf, &logBuf, &logBuf))
	log.SetOutput(&logBuf)

	root := &c2CloseCounter{}
	hi := NewHandshakeInfo(root, nil, nil, false, "", false, false)
	if got := hi.refs.Load(); got != 1 {
		t.Fatalf("initial refs = %d, want 1", got)
	}
	hi.Release() // owner's final valid release
	if got := hi.refs.Load(); got != 0 || root.closes != 1 {
		t.Fatalf("after final release: refs=%d closes=%d, want 0 and 1", got, root.closes)
	}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				t.Logf("extra Release panicked: %v", r)
			}
		}()
		hi.Release() // extra, invalid release
	}()
	refs := hi.refs.Load()
	t.Logf("after extra Release: refs=%d closes=%d panicked=%v acquireOK=%v logged=%q", refs, root.closes, panicked, hi.Acquire(), logBuf.String())
	if refs < 0 && !panicked && logBuf.Len() == 0 {
		t.Fatalf("extra Release returned normally with refs=%d and no misuse report", refs)
	}
}
