// Run: git worktree add --detach /tmp/wt-0e684571 <claims-remote>/evalon/grpc-go-tr-0e684571 && cp verify/repro/c3_worker_teardown_test.go /tmp/wt-0e684571/internal/transport/verify_c3_worker_teardown_test.go && cd /tmp/wt-0e684571 && for sc in setup reception success; do VERIFY_C3_SCENARIO=$sc go test -v -run '^TestVerify_C3_WorkerAfterCleanup$' ./internal/transport -count=1; done
//
// Drives the branch's own, unmodified startTinyDataFrameServer fixture down a
// failure path inside a subtest. t.Run returns only after the subtest's
// cleanups have completed, so any fixture goroutine seen after t.Run returns
// outlived fixture cleanup. (Expected to FAIL/panic: that is the evidence.)

package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/resolver"
)

var verifyC3WorkerRE = regexp.MustCompile(`startTinyDataFrameServer\.func\d+(\.\d+)?\b`)

// verifyC3Workers returns the fixture goroutines currently alive, described by
// the fixture closure they run and where they are parked.
func verifyC3Workers() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		// Only goroutines *running* a fixture closure, not the test goroutine
		// that merely created one.
		created := strings.Index(g, "created by")
		body := g
		if created >= 0 {
			body = g[:created]
		}
		m := verifyC3WorkerRE.FindString(body)
		if m == "" {
			continue
		}
		lines := strings.Split(g, "\n")
		top := ""
		if len(lines) > 1 {
			top = strings.TrimSpace(lines[1])
		}
		out = append(out, m+" ["+strings.TrimSuffix(strings.SplitN(lines[0], "[", 2)[1], "]:")+"] at "+top)
	}
	return out
}

func TestVerify_C3_WorkerAfterCleanup(t *testing.T) {
	scenario := os.Getenv("VERIFY_C3_SCENARIO")
	var before []string
	t.Run("fixture", func(t *testing.T) {
		switch scenario {
		case "setup":
			// Setup failure: the client transport cannot be created (here: its
			// connect context is already done), exactly the test's
			// `t.Fatalf("NewHTTP2Client failed: %v", err)` branch.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			addr, _ := startTinyDataFrameServer(ctx, t, 60000)
			dead, kill := context.WithCancel(context.Background())
			kill()
			_, err := NewHTTP2Client(dead, ctx, resolver.Address{Addr: addr}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})
			if err == nil {
				t.Fatal("injection failed: NewHTTP2Client succeeded")
			}
			before = verifyC3Workers()
			t.Fatalf("NewHTTP2Client failed: %v", err)
		case "reception":
			// Reception failure: the peer stops reading, so the fixture's
			// writer worker blocks in Write and the fixture's own
			// waitForServer hits its ctx.Done() t.Fatalf branch.
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			addr, waitForServer := startTinyDataFrameServer(ctx, t, 20_000_000)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.Write(clientPreface)
			fr := http2.NewFramer(conn, conn)
			fr.WriteSettings()
			fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: []byte{0x83}, EndHeaders: true})
			time.Sleep(1000 * time.Millisecond) // let the writer fill the socket and block
			before = verifyC3Workers()
			waitForServer()
		case "success":
			// Control: the happy path of the branch's own test.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			addr, waitForServer := startTinyDataFrameServer(ctx, t, 1000)
			ct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: addr}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})
			if err != nil {
				t.Fatal(err)
			}
			defer ct.Close(errors.New("test cleanup"))
			stream, err := ct.NewStream(ctx, &CallHdr{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			waitForServer()
			<-stream.Done()
			if _, err := stream.readTo(make([]byte, 1000)); err != nil && err != io.EOF {
				t.Fatal(err)
			}
			before = verifyC3Workers()
		default:
			t.Fatalf("set VERIFY_C3_SCENARIO=setup|reception|success")
		}
	})
	// The subtest, including every t.Cleanup it registered, has completed.
	after := verifyC3Workers()
	os.Stderr.WriteString("RESULT C3 scenario=" + scenario + "\n")
	for _, w := range before {
		os.Stderr.WriteString("  worker alive at failure point (before cleanup): " + w + "\n")
	}
	os.Stderr.WriteString("  workers alive after fixture cleanup completed: " + itoa(len(after)) + "\n")
	for _, w := range after {
		os.Stderr.WriteString("  worker alive AFTER fixture cleanup completed: " + w + "\n")
	}
	start := time.Now()
	for len(verifyC3Workers()) > 0 && time.Since(start) < 3*time.Second {
		time.Sleep(50 * time.Microsecond)
	}
	os.Stderr.WriteString("  workers gone " + time.Since(start).String() + " after cleanup completed (remaining=" + itoa(len(verifyC3Workers())) + ")\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
