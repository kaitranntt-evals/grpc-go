//go:build ignore

// Audit probe for C1 (branch 9eec450b). Run: go test -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
package transport

import (
	"net"
	"sync"
	"testing"
)


// Mirrors TestServerStream_ReadsMessageSplitIntoTinyFrames defer order: `defer lis.Close()` then `defer wg.Wait()`
// (wg.Wait runs first), with the accept goroutine blocked in lis.Accept(). Failure path: net.Dial fails -> t.Fatalf.
func TestVerifyC1Stall(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()
	closed, _ := net.Listen("tcp", "localhost:0")
	addr := closed.Addr().String()
	closed.Close()
	conn, err := net.Dial("tcp", addr) // simulated dial failure
	if err != nil {
		t.Logf("net.Dial() failed: %v -- now running deferred wg.Wait() before lis.Close()", err)
		t.Fatalf("net.Dial() failed: %v", err)
	}
	conn.Close()
}
