// C2 instrumentation shared by the three branch patches in this directory (copied to internal/transport/ by c2_apply.py).
// VERIFY_STALL selects which un-deadlined fake-server socket operation is made to stall:
//
//	headers   - after the client's HEADERS arrive the fake server never answers and blocks in a socket read
//	handshake - the fake server accepts, then blocks in a socket read instead of sending SETTINGS
//	accept    - the client is pointed at a second listener nobody accepts on, so lis.Accept() never returns on its own
//	headers-main-blocked - like headers, and additionally the test goroutine is parked for 25s right after NewStream,
//	                       so none of its deferred Close calls can run in the meantime
//	accept-main-blocked  - like accept, and additionally the test goroutine is parked for 25s just before it calls
//	                       NewHTTP2Client (after its 10s contexts were created), so its deferred lis.Close() cannot run
package transport

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

var verifyC2Start = time.Now()

func verifyC2Stall() string {
	switch s := os.Getenv("VERIFY_STALL"); s {
	case "headers-main-blocked":
		return "headers"
	case "accept-main-blocked":
		return "accept"
	default:
		return s
	}
}

// verifyC2MaybeBlockMain parks the test goroutine (so its deferred cleanup
// cannot run) to show what interrupts the fake server's stalled read on its own.
func verifyC2MaybeBlockMain() {
	if os.Getenv("VERIFY_STALL") != "headers-main-blocked" {
		return
	}
	verifyC2Logf("test goroutine: parked for 25s; no deferred Close can run until it wakes")
	time.Sleep(25 * time.Second)
	verifyC2Logf("test goroutine: resumed")
}

func verifyC2Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "VERIFY C2 [t+%5.1fs] %s\n", time.Since(verifyC2Start).Seconds(), fmt.Sprintf(format, args...))
}

// verifyC2StalledRead blocks in a socket read that has no deadline, exactly
// like the fake server's own ReadFrame/ReadFull calls, and reports when and
// how it was interrupted.
func verifyC2StalledRead(conn net.Conn, where string) error {
	start := time.Now()
	verifyC2Logf("fake server: entering stalled socket read (%s), no deadline set", where)
	n, err := io.ReadFull(conn, make([]byte, 1<<20))
	verifyC2Logf("fake server: stalled socket read (%s) was interrupted after %.1fs: n=%d err=%v", where, time.Since(start).Seconds(), n, err)
	return fmt.Errorf("verify: stalled read ended: %v", err)
}

// verifyC2DialAddr returns the address the client should dial.
func verifyC2DialAddr(lis net.Listener) string {
	if verifyC2Stall() != "accept" {
		return lis.Addr().String()
	}
	lis2, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}
	verifyC2Logf("client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)")
	if os.Getenv("VERIFY_STALL") == "accept-main-blocked" {
		verifyC2Logf("test goroutine: parked for 25s; no deferred Close can run until it wakes")
		time.Sleep(25 * time.Second)
		verifyC2Logf("test goroutine: resumed")
	}
	return lis2.Addr().String()
}

func verifyC2ServerReturned(start time.Time, err error) {
	verifyC2Logf("fake server goroutine returned %.1fs after it started: err=%v", time.Since(start).Seconds(), err)
}
