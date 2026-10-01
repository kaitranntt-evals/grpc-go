#!/usr/bin/env python3
# How to run: python3 verify/probes/c2/c2_apply.py <worktree> <c15666d0|7b7bc477|9841642e>   (then: VERIFY_STALL=headers|handshake|accept go test -v -run 'Test/<name>' google.golang.org/grpc/internal/transport -count=1)
# Applies the C2 stall-injection mutations (test files only) to a checkout of the given claim branch.
import sys, shutil, os
wt, br = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
shutil.copy(os.path.join(here, 'verify_c2_helpers_test.go'), os.path.join(wt, 'internal/transport/verify_c2_helpers_test.go'))

def rep(s, a, b, count=1):
    assert s.count(a) >= 1, a
    return s.replace(a, b, count)

if br == 'c15666d0':
    p = os.path.join(wt, 'internal/transport/recv_buffer_test.go')
    s = open(p).read()
    s = rep(s, '''			serverErr <- serveTinyDataFrames(lis, numFrames)''',
'''			verifyStart := time.Now()
			verifyErr := serveTinyDataFrames(lis, numFrames)
			verifyC2ServerReturned(verifyStart, verifyErr)
			serverErr <- verifyErr''')
    s = rep(s, '''resolver.Address{Addr: lis.Addr().String()}''', '''resolver.Address{Addr: verifyC2DialAddr(lis)}''')
    s = rep(s, '''	bw := bufio.NewWriter(conn)
	fr := http2.NewFramer(bw, conn)
''', '''	bw := bufio.NewWriter(conn)
	fr := http2.NewFramer(bw, conn)
	if verifyC2Stall() == "handshake" {
		return verifyC2StalledRead(conn, "instead of sending SETTINGS")
	}
''')
    s = rep(s, '''		var payload int
		for ctx.Err() == nil {''', '''		verifyC2MaybeBlockMain()
		var payload int
		for ctx.Err() == nil {''')
    s = rep(s, '''		case *http2.HeadersFrame:
			var hbuf bytes.Buffer''', '''		case *http2.HeadersFrame:
			if verifyC2Stall() == "headers" {
				return verifyC2StalledRead(conn, "instead of answering HEADERS")
			}
			var hbuf bytes.Buffer''')
    open(p, 'w').write(s)
elif br == '9841642e':
    p = os.path.join(wt, 'internal/transport/recv_buffer_test.go')
    s = open(p).read()
    s = rep(s, '''				serverErr <- serveTinyDataFrames(lis, numFrames)''',
'''				verifyStart := time.Now()
				verifyErr := serveTinyDataFrames(lis, numFrames)
				verifyC2ServerReturned(verifyStart, verifyErr)
				serverErr <- verifyErr''')
    s = rep(s, '''resolver.Address{Addr: lis.Addr().String()}''', '''resolver.Address{Addr: verifyC2DialAddr(lis)}''')
    s = rep(s, '''	w := bufio.NewWriter(sconn)
	sfr := http2.NewFramer(w, sconn)
''', '''	w := bufio.NewWriter(sconn)
	sfr := http2.NewFramer(w, sconn)
	if verifyC2Stall() == "handshake" {
		return verifyC2StalledRead(sconn, "instead of sending SETTINGS")
	}
''')
    s = rep(s, '''			var entries, payload int
			for deadline := time.Now()''', '''			verifyC2MaybeBlockMain()
			var entries, payload int
			for deadline := time.Now()''')
    s = rep(s, '''		case *http2.HeadersFrame:
			var buf bytes.Buffer''', '''		case *http2.HeadersFrame:
			if verifyC2Stall() == "headers" {
				return verifyC2StalledRead(sconn, "instead of answering HEADERS")
			}
			var buf bytes.Buffer''')
    open(p, 'w').write(s)
elif br == '7b7bc477':
    p = os.path.join(wt, 'internal/transport/recvbuffer_test.go')
    s = open(p).read()
    s = rep(s, '''		serverErr <- func() error {
			sconn, err := lis.Accept()''', '''		verifyStart := time.Now()
		serverErr <- func() (verifyErr error) {
			defer func() { verifyC2ServerReturned(verifyStart, verifyErr) }()
			sconn, err := lis.Accept()''')
    s = rep(s, '''resolver.Address{Addr: lis.Addr().String()}''', '''resolver.Address{Addr: verifyC2DialAddr(lis)}''')
    s = rep(s, '''			w := bufio.NewWriter(sconn)
			sfr := http2.NewFramer(w, sconn)
''', '''			w := bufio.NewWriter(sconn)
			sfr := http2.NewFramer(w, sconn)
			if verifyC2Stall() == "handshake" {
				return verifyC2StalledRead(sconn, "instead of sending SETTINGS")
			}
''')
    s = rep(s, '''			go func() {
				for {
					if _, err := sfr.ReadFrame(); err != nil {''', '''			if verifyC2Stall() == "headers" {
				return verifyC2StalledRead(sconn, "instead of answering HEADERS")
			}
			go func() {
				for {
					if _, err := sfr.ReadFrame(); err != nil {''')
    s = rep(s, '''	select {
	case <-cs.Done():''', '''	verifyC2MaybeBlockMain()
	select {
	case <-cs.Done():''')
    if '\t"time"\n' not in s:
        s = rep(s, '''	"testing"
''', '''	"testing"
	"time"
''')
    open(p, 'w').write(s)
else:
    sys.exit('unknown branch')
