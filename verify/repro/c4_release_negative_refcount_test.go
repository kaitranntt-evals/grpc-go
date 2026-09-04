// Run (on branch 002b71aa): cp verify/repro/c4_release_negative_refcount_test.go internal/credentials/xds/zz_verify_c4_test.go && go test ./internal/credentials/xds -run 'TestVerifyC4_' -count=1 -v
//
// Creates a HandshakeInfo, releases its only valid (owner) reference, then performs one extra
// unmatched Release and reports the resulting internal reference count, whether anything was
// logged through grpclog, and whether a panic occurred.

package xds

import (
	"bytes"
	"context"
	"os"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/grpclog"
)

type verifyC4Provider struct{ closed atomic.Int32 }

func (p *verifyC4Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *verifyC4Provider) Close() { p.closed.Add(1) }

func TestVerifyC4_ReleaseBelowZero(t *testing.T) {
	var logBuf bytes.Buffer
	prev := grpclog.Component("verify") // force logger init before swapping
	_ = prev
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(&logBuf, &logBuf, &logBuf))
	defer grpclog.SetLoggerV2(grpclog.NewLoggerV2(os.Stderr, os.Stderr, os.Stderr))

	root := &verifyC4Provider{}
	hi := NewHandshakeInfo(root, nil, nil, false, "", false, false)
	t.Logf("after NewHandshakeInfo: refs=%d", hi.refs.Load())

	hi.Release() // releases the single valid (owner) reference
	t.Logf("after 1st Release (all valid refs released): refs=%d rootProvider.Close() calls=%d", hi.refs.Load(), root.closed.Load())

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				t.Logf("extra Release panicked: %v", r)
			}
		}()
		hi.Release() // unmatched extra release
	}()
	t.Logf("after extra Release: refs=%d rootProvider.Close() calls=%d panicked=%v grpclog output=%q", hi.refs.Load(), root.closed.Load(), panicked, logBuf.String())
	t.Logf("Acquire() after extra Release: %v", hi.Acquire())

	if hi.refs.Load() < 0 && !panicked && logBuf.Len() == 0 {
		t.Errorf("CONFIRMED: reference count fell to %d after an unmatched Release with no panic, error, log diagnostic, or rejection", hi.refs.Load())
	}
}
