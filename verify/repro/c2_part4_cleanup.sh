#!/bin/bash
# Run: verify/repro/c2_part4_cleanup.sh     (targets evalon/grpc-go-xd-545b9364)
# C2 "resource cleanup registration" probe (injected failure). In TestServerSideXDS_FilterStateRetention_RDSUpdates
# the fatal assertion that sits between xds.NewGRPCServer(...) and `defer stopServer()` (the LocalTCPListener error
# check) is made to fire. mode=inject: only that. mode=control: same injected failure, plus `defer server.Stop()`
# registered right after the server is created. File restored afterwards.
source "$(dirname "$0")/common.sh"
d=$(dir_of 545b9364); cd "$d" || exit 2
f=test/xds/xds_server_filter_state_retention_test.go
cp "$f" "$OUT/c2p4.545b9364.orig"
for mode in inject control; do
  cp "$OUT/c2p4.545b9364.orig" "$f"
  log="$OUT/c2p4.545b9364.$mode.log"
  perl -0pi -e 's/(\tlis, err := testutils\.LocalTCPListener\(\)\n)(\tif err != nil \{\n\t\tt\.Fatal\(err\)\n\t\}\n\tstubserver\.StartTestService\(t, &stubserver\.StubServer\{\n\t\tS: server, Listener: lis,)/$1\tif err == nil {\n\t\tlis.Close()\n\t\terr = fmt.Errorf("VERIFY-MUTATION: injected LocalTCPListener failure")\n\t}\n$2/' "$f"
  if [ $mode = control ]; then
    perl -0pi -e 's/(\tlis, err := testutils\.LocalTCPListener\(\)\n\tif err == nil \{)/\tdefer server.Stop() \/\/ VERIFY-CONTROL: cleanup registered before the fatal assertion\n$1/' "$f"
  fi
  echo "mode=$mode mutation lines applied: $(grep -c 'VERIFY-' "$f")" > "$log"
  go test -count=1 -timeout 60s -v -run '^Test$/^ServerSideXDS_FilterStateRetention_RDSUpdates$' ./test/xds >> "$log" 2>&1; echo "exit=$?" >> "$log"
  echo "leaked goroutines reported: $(grep -c 'Leaked goroutine' "$log")" >> "$log"
  grep -E "^mode=|VERIFY-MUTATION|Leaked goroutine|Found [0-9]+ leaked|^\s*--- |^exit=|leaked goroutines reported" "$log" | grep -v "tlogger.go"
done
cp "$OUT/c2p4.545b9364.orig" "$f"
echo "worktree dirty files after restore: $(git status --short | grep -vc '^??')"
