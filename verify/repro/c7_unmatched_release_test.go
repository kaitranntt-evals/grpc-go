// Run: cp verify/repro/c7_unmatched_release_test.go internal/credentials/xds/ && go test ./internal/credentials/xds/ -run 'Test/VerifyC7' -count=1 -v   (on branch evalon/grpc-go-xd-abb83a21)

package xds

import (
	"bytes"
	"context"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/grpclog"
)

type c7CountingProvider struct{ closes int }

func (p *c7CountingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *c7CountingProvider) Close() { p.closes++ }

// Performs an unmatched Release after the count reached zero, capturing all
// grpclog output and recovering any panic.
func (s) TestVerifyC7_UnmatchedReleaseGoesNegativeSilently(t *testing.T) {
	var logBuf bytes.Buffer
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(&logBuf, &logBuf, &logBuf, 99))

	root := &c7CountingProvider{}
	hi := NewHandshakeInfo(root, nil, nil, false, "", false, false)
	hi.Release() // matched: owner reference, count 1 -> 0
	t.Logf("after matched Release: refs=%d closes=%d", hi.refs.Load(), root.closes)

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		hi.Release() // unmatched
	}()
	t.Logf("after unmatched Release: refs=%d closes=%d panic=%v log=%q", hi.refs.Load(), root.closes, panicked, logBuf.String())
	t.Logf("Acquire() after unmatched Release = %v", hi.Acquire())

	if hi.refs.Load() < 0 && panicked == nil && logBuf.Len() == 0 {
		t.Fatalf("unmatched Release drove refs to %d with no log, panic, or error", hi.refs.Load())
	}
}
