#!/bin/sh
# Run: sh verify/repro/c1_zero_copy_unbounded_receive.sh   (from anywhere inside the repo; needs go + network access to the claim repo; ~2.5 min)
# C1: TestRecvBufferZeroCopy's bare `<-b.get()` receives have no local bound; when queue progress stalls only the global `go test -timeout` alarm ends the run.
. "$(git rev-parse --show-toplevel)/verify/repro/_common.sh"
claim_worktree evalon/grpc-go-tr-f5eba3f9

echo "== 0. the receives under test"
run grep -n 'b.get()' internal/transport/recv_buffer_test.go
run sed -n '164,166p' internal/transport/transport.go
run grep -n 'defaultTestTimeout = ' internal/transport/keepalive_test.go

echo "== 1. baseline: the test is exercised and passes on the unmodified branch"
go test -v -run 'Test/RecvBufferZeroCopy$' ./internal/transport -count=1 2>&1 | grep -E '^(---|ok|FAIL|PASS)|--- (PASS|FAIL): Test/RecvBufferZeroCopy \('

stall() { # $1 = patch, $2 = test regexp, $3 = -timeout
	git apply "$REPRO/$1"
	start=$(date +%s)
	go test -run "$2" ./internal/transport -count=1 -timeout "$3" >"$WT/out.txt" 2>&1 || true
	end=$(date +%s)
	git checkout -q internal/transport/transport.go
	echo "(wall clock: $((end - start))s, go test -timeout $3)"
}

echo "== 2. mutation c1_stall_load.patch (load() never promotes the backlog): second receive, recv_buffer_test.go:172"
stall c1_stall_load.patch 'Test/RecvBufferZeroCopy$' 45s
grep -E 'panic: test timed out|^\s+Test.*\([0-9]+s\)$|^(ok|FAIL|---)' "$WT/out.txt" || true
grep -B1 -A1 'TestRecvBufferZeroCopy.func1.1' "$WT/out.txt" || true

echo "== 3. contrast, same mutation: TestRecvBufferCompactionOrdering reads through a recvBufferReader with a defaultTestTimeout context and stops by itself"
stall c1_stall_load.patch 'Test/RecvBufferCompactionOrdering$' 45s
grep -E 'panic: test timed out|--- FAIL|recv_buffer_test.go|^(ok|FAIL)' "$WT/out.txt" || true

echo "== 4. mutation c1_stall_put.patch (put() never hands a message straight to b.c): first receive, recv_buffer_test.go:166"
stall c1_stall_put.patch 'Test/RecvBufferZeroCopy$' 30s
grep -E 'panic: test timed out|^\s+Test.*\([0-9]+s\)$|^(ok|FAIL|---)' "$WT/out.txt" || true
grep -B1 -A1 'TestRecvBufferZeroCopy.func1.1' "$WT/out.txt" || true

run git status --short
