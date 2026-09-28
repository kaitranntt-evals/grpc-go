//go:build ignore

// Audit probes for C1 refutations (ae28a1c4, f0f841e2, bd3f54f4, fd3461ea, eb8d8e69, c25e344e, 9dc4bb9a). Run: go test -v -run '^TestVerifyC1Refute' -timeout 60s -count=1 ./internal/transport
package transport

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// ae28a1c4: net.Dial("tcp", loopback) without a timeout -- can it stall?
func TestVerifyC1Refute_DialToLoopback(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	start := time.Now()
	conn, err := net.Dial("tcp", lis.Addr().String()) // listener never calls Accept
	t.Logf("Dial to listening-but-never-accepting peer: err=%v after %v", err, time.Since(start))
	if conn != nil {
		conn.Close()
	}
	addr := lis.Addr().String()
	lis.Close()
	start = time.Now()
	_, err = net.Dial("tcp", addr)
	t.Logf("Dial to closed listener: err=%v after %v", err, time.Since(start))
}

// f0f841e2/bd3f54f4/fd3461ea/eb8d8e69/c25e344e: raw framer writes with no deadline against a peer that never reads.
func TestVerifyC1Refute_RawWritesToNonReadingPeer(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := lis.Accept()
		if err == nil {
			accepted <- c // accepted but never read from
		}
	}()
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fr := http2.NewFramer(conn, nil)
	for _, frames := range []int{8192, 10000, 32768, 32845, 65536} { // volumes used by the audited tests (1-byte DATA frames = 10 bytes on the wire)
		start := time.Now()
		for i := 0; i < frames; i++ {
			if err := fr.WriteData(1, false, []byte{byte(i)}); err != nil {
				t.Fatalf("WriteData: %v", err)
			}
		}
		t.Logf("%d one-byte DATA frames (%d wire bytes) written to a never-reading peer in %v (cumulative)", frames, frames*10, time.Since(start))
	}
	select {
	case c := <-accepted:
		c.Close()
	default:
	}
}

// 9dc4bb9a: server goroutine loops on framer.ReadFrame(); does closing the client transport's conn unblock it?
func TestVerifyC1Refute_ReadFrameUnblocksOnPeerClose(t *testing.T) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fr := http2.NewFramer(conn, conn)
		for {
			if _, err := fr.ReadFrame(); err != nil {
				return
			}
		}
	}()
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	conn.Close() // what ct.Close(...) does before `<-serverDone`
	<-serverDone
	t.Logf("<-serverDone returned %v after closing the client conn", time.Since(start))
}
