// Run (on branch 53e1cbd8): cp verify/repro/c6_tight_retry_loop_test.go credentials/xds/zz_verify_c6_test.go && go test ./credentials/xds -run 'TestVerifyC6_' -count=1 -v
//
// Publishes a HandshakeInfo, retires it with Close() while leaving it published, then calls
// ClientHandshake with a context that expires after 300ms. It reports whether ClientHandshake
// returned within 2s of the deadline, how much user CPU the process burned while waiting, and the
// goroutine stack of the handshake at that time.

package xds

import (
	"context"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/resolver"
)

func verifyC6UserCPU(t *testing.T) time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatalf("Getrusage: %v", err)
	}
	return time.Duration(ru.Utime.Nano())
}

func TestVerifyC6_RetiredSnapshotStillPublished(t *testing.T) {
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatalf("NewClientCredentials failed: %v", err)
	}

	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hi := xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, nil, false, "", false, false)
	hiPtr.Store(hi)
	hi.Close() // retire the snapshot; it stays published in hiPtr
	t.Logf("retired snapshot still published: hiPtr.Load()==hi is %v; hi.Acquire()=%v", hiPtr.Load() == hi, hi.Acquire())

	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	done := make(chan error, 1)
	start := time.Now()
	cpu0 := verifyC6UserCPU(t)
	go func() {
		_, _, err := creds.ClientHandshake(hsCtx, "localhost", c1)
		done <- err
	}()

	select {
	case err := <-done:
		t.Logf("ClientHandshake returned after %v with err=%v (ctx.Err()=%v)", time.Since(start), err, ctx.Err())
		return
	case <-time.After(2 * time.Second):
	}
	cpu := verifyC6UserCPU(t) - cpu0
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	var frame string
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "ClientHandshake") {
			frame = g
			break
		}
	}
	t.Logf("ClientHandshake has NOT returned %v after start (ctx.Err()=%v); user CPU consumed by the process in that window: %v", time.Since(start), ctx.Err(), cpu.Round(time.Millisecond))
	t.Logf("handshake goroutine stack:\n%s", frame)
	t.Errorf("CONFIRMED: ClientHandshake did not return within 2s although the context expired after 300ms; it is spinning on hiPtr.Load()/Acquire()")

	// Let the spinning goroutine escape so the test binary can exit: unpublish the snapshot.
	hiPtr.Store(nil)
	select {
	case err := <-done:
		t.Logf("after hiPtr.Store(nil), ClientHandshake returned: err=%v", err)
	case <-time.After(2 * time.Second):
		t.Logf("ClientHandshake still not returned 2s after unpublishing")
	}
}
