// Run: sh verify/repro/c2_stop_wait.sh  (copies this file to internal/transport/ of evalon/grpc-go-tr-92749eaf and runs it)

package transport

import (
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc/mem"
)

// stopGoroutineStack returns the stack of the goroutine currently inside
// (*server).stop, or "" if there is none.
func stopGoroutineStack() string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "transport.(*server).stop(") {
			return g
		}
	}
	return ""
}

// Probes the cleanup mechanism of the pre-existing test helper server.stop()
// that the added integration test defers: with one serving goroutine
// (HandleStreams) still alive, stop() stays parked on <-s.servingTasksDone
// past every timeout the helper configures locally (the per-connection context
// created in server.start uses defaultTestTimeout), and returns only once the
// serving goroutine exits.
func (s) TestVerifyC2_StopWaitHasNoLocalBound(t *testing.T) {
	// Same server flavour as the added TestServerStreamReceivesManyTinyDataFrames.
	server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)

	// A raw HTTP/2 client that keeps its connection open until told otherwise
	// (the test's *http2Client closes itself when its 10s context expires,
	// which would end the serving goroutine and hide what is being probed).
	conn, err := net.Dial("tcp", server.lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() failed: %v", err)
	}
	defer conn.Close()
	go io.Copy(io.Discard, conn)
	if _, err := conn.Write(clientPreface); err != nil {
		t.Fatalf("Writing client preface failed: %v", err)
	}
	if err := http2.NewFramer(conn, conn).WriteSettings(); err != nil {
		t.Fatalf("WriteSettings() failed: %v", err)
	}

	// Wait for the server transport to be registered, then take it out of the
	// set stop() closes. Its HandleStreams goroutine therefore keeps running
	// for as long as the client keeps the connection open, i.e. a serving
	// task that does not complete on its own.
	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if len(server.conns) == 0 {
			return true, fmt.Errorf("timed-out while waiting for connection to be created on the server")
		}
		return false, nil
	})
	server.mu.Lock()
	for k := range server.conns {
		delete(server.conns, k)
	}
	server.mu.Unlock()

	stopReturned := make(chan struct{})
	start := time.Now()
	go func() {
		server.stop()
		close(stopReturned)
	}()

	// defaultTestTimeout (10s) is the only timeout the helper configures; wait
	// three times as long.
	const observe = 3 * defaultTestTimeout
	select {
	case <-stopReturned:
		t.Fatalf("VERIFY-C2 server.stop() returned after %v although a serving goroutine was still running", time.Since(start))
	case <-time.After(observe):
	}
	t.Logf("VERIFY-C2 server.stop() still blocked after %v (defaultTestTimeout = %v). Goroutine:\n%s", time.Since(start).Round(time.Millisecond), defaultTestTimeout, stopGoroutineStack())

	// Let the serving goroutine finish: only this releases stop().
	releasedAt := time.Now()
	conn.Close()
	select {
	case <-stopReturned:
		t.Logf("VERIFY-C2 server.stop() returned %v after the serving goroutine was allowed to exit (total blocked: %v)", time.Since(releasedAt).Round(time.Microsecond), time.Since(start).Round(time.Millisecond))
	case <-time.After(defaultTestTimeout):
		t.Fatalf("server.stop() did not return after the client connection was closed")
	}
}
