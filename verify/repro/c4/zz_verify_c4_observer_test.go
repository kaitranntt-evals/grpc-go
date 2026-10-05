// Run (on branch evalon/grpc-go-tr-9e4507fd): see run_c4.sh in this directory.
// Top-level test that runs after the grpctest suite `Test` has returned (file
// name sorts last) and reports goroutines still executing serveTinyDataFrames.

package transport

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestZZVerifyC4_Observer(t *testing.T) {
	time.Sleep(3 * time.Second) // let any worker that is going to exit do so
	buf := make([]byte, 8<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "serveTinyDataFrames") {
			n++
			t.Logf("VERIFY-C4 worker still alive after the suite finished:\n%s", g)
		}
	}
	t.Logf("VERIFY-C4 RESULT: %d serveTinyDataFrames goroutine(s) alive %v after suite end", n, 3*time.Second)
}
