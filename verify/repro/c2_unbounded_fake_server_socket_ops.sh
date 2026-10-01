#!/usr/bin/env bash
# How to run: verify/repro/c2_unbounded_fake_server_socket_ops.sh <checkout of the claim branch> <c15666d0|7b7bc477|9841642e> [stall modes...]   (default modes: none accept-main-blocked headers; test-file-only instrumentation, reverted afterwards)
#
# C2: are the fake HTTP/2 server's socket operations in the branch's added
# transport test bounded by a socket deadline or an independent timed
# cancellation? The script injects a stall into one of those operations
# (verify/probes/c2/) and logs when, and by what, the stalled call is released.
set -euo pipefail
wt=$(cd "$1" && pwd); br=$2; shift 2
modes=("$@"); [ ${#modes[@]} -gt 0 ] || modes=(none accept-main-blocked headers)
here=$(cd "$(dirname "$0")" && pwd)
case $br in
  c15666d0) name=ClientTransport_SlowReaderTinyDataFrames; file=recv_buffer_test.go ;;
  9841642e) name=ClientTinyDataFramesReceiveMemory; file=recv_buffer_test.go ;;
  7b7bc477) name=ClientStream_ManyTinyDataFramesSlowReader; file=recvbuffer_test.go ;;
  *) echo "unknown branch $br" >&2; exit 2 ;;
esac
echo "\$ grep -c 'SetDeadline\|SetReadDeadline\|SetWriteDeadline\|DialTimeout\|AfterFunc' internal/transport/$file"
grep -c 'SetDeadline\|SetReadDeadline\|SetWriteDeadline\|DialTimeout\|AfterFunc' "$wt/internal/transport/$file" || true
python3 "$here/../probes/c2/c2_apply.py" "$wt" "$br"
trap 'cd "$wt" && git checkout -- internal/transport && rm -f internal/transport/verify_c2_helpers_test.go' EXIT
cd "$wt"
for st in "${modes[@]}"; do
  echo "\$ VERIFY_STALL=$st go test -v -run 'Test/$name\$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s"
  VERIFY_STALL=$st go test -v -run "Test/$name\$" google.golang.org/grpc/internal/transport -count=1 -timeout 180s 2>&1 |
    grep -E 'VERIFY C2|_test.go:[0-9]+:|^--- |^    --- |^(ok|FAIL)[[:space:]]' || true
done
