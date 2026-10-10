// Run: git checkout evalon/grpc-go-xd-7ea015db (repo kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak); cp verify/repro/c3_integration_cleanup_test.go test/xds/ && git apply verify/repro/c3_mutation_reintroduce_leak.patch && go test -count=1 -run '^TestVerifyC3$' ./test/xds | grep VC3   (omit the 'git apply' for the passing control)

package xds_test

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// vc3Sockets returns the local ports of the TCP sockets in LISTEN state owned
// by this process, and the total number of TCP sockets (any state) it owns.
func vc3Sockets() (listen []int, total int) {
	inodes := map[string]bool{}
	fds, _ := os.ReadDir("/proc/self/fd")
	for _, fd := range fds {
		if l, err := os.Readlink("/proc/self/fd/" + fd.Name()); err == nil && strings.HasPrefix(l, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")] = true
		}
	}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, _ := os.ReadFile(f)
		for _, ln := range strings.Split(string(b), "\n") {
			fs := strings.Fields(ln)
			if len(fs) < 10 || !inodes[fs[9]] {
				continue
			}
			total++
			if fs[3] == "0A" {
				p, _ := strconv.ParseInt(fs[1][strings.LastIndex(fs[1], ":")+1:], 16, 32)
				listen = append(listen, int(p))
			}
		}
	}
	sort.Ints(listen)
	return listen, total
}

// vc3Observe runs fn as a subtest and reports which sockets acquired by it
// are still open after the subtest (including all of its deferred functions
// and t.Cleanup callbacks) has exited.
func vc3Observe(t *testing.T, name string, fn func(*testing.T)) {
	beforeListen, beforeTotal := vc3Sockets()
	passed := t.Run(name, fn)
	diff := func() (leaked []int, total int) {
		afterListen, total := vc3Sockets()
		for _, p := range afterListen {
			if i := sort.SearchInts(beforeListen, p); i == len(beforeListen) || beforeListen[i] != p {
				leaked = append(leaked, p)
			}
		}
		return leaked, total
	}
	// First sample: immediately after the subtest exited.
	leaked, afterTotal := diff()
	fmt.Printf("VC3 %-64s passed=%-5v atExit: listenersStillOpen=%v tcpSockets(before=%d after=%d)\n", name, passed, leaked, beforeTotal, afterTotal)
	for _, p := range leaked {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), time.Second)
		fmt.Printf("VC3   dial still-open listener 127.0.0.1:%d after the test exited: err=%v\n", p, err)
		if err == nil {
			c.Close()
		}
	}
	// Second sample: after allowing asynchronous teardown (and, unless
	// GOGC=off, garbage-collector finalizers) up to 3s to run.
	for deadline := time.Now().Add(3 * time.Second); (len(leaked) > 0 || afterTotal > beforeTotal) && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		leaked, afterTotal = diff()
	}
	fmt.Printf("VC3 %-64s passed=%-5v settled(<=3s): listenersStillOpen=%v tcpSockets(before=%d after=%d)\n", name, passed, leaked, beforeTotal, afterTotal)
}

func TestVerifyC3(t *testing.T) {
	vc3Observe(t, "TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates", s{}.TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates)
}
