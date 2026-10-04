#!/usr/bin/env bash
# Run: verify/repro/c1_blocking_ops.sh <1e8d9a91|8c775707|786c4c95|0973d83c|e11b3799> <path to a git worktree of evalon/grpc-go-tr-<hash>>  (restores the worktree afterwards)
set -u
hash=$1; wt=$(cd "$2" && pwd); here=$(cd "$(dirname "$0")" && pwd)
pkg=internal/transport
cleanup() {
  cd "$wt" && git checkout -q -- $pkg/http2_client.go $pkg/transport.go
  rm -f $pkg/verify_c1_watch_test.go $pkg/verify_c1_stall.go $pkg/verify_c1_probe_test.go
}
trap cleanup EXIT
cd "$wt" || exit 2
echo "### branch tip: $(git log -1 --format='%H %s')"

# Fault 1 (network branches): the client never reaches the authored raw server,
# so the authored lis.Accept() stalls. dial() blocks until its ctx ends.
stall_dial() {
  cp "$here/c1_stall_dial.go.txt" $pkg/verify_c1_stall.go
  sed -i 's|^func dial(ctx context.Context, fn func(context.Context, string) (net.Conn, error), addr resolver.Address, grpcUA string) (net.Conn, error) {$|&\n\tif err := verifyC1StallDial(ctx); err != nil {\n\t\treturn nil, err\n\t}|' $pkg/http2_client.go
  git diff --stat -- $pkg/http2_client.go | cat
}
watch() { cp "$here/c1_stall_watch_test.go.txt" $pkg/verify_c1_watch_test.go; }
run() { echo "+ $*"; ( time env "$@" ) 2>&1 | grep -v '^$' | grep -v '^\(user\|sys\)' ; }

case $hash in
1e8d9a91)
  echo "## authored helper"; grep -n 'func newTestRecvStream' -A8 $pkg/recv_buffer_test.go
  echo "## callers of the helper / of readAllFromStream"; grep -n 'newTestRecvStream()\|readAllFromStream(\|st.read(\|WithTimeout\|ctxDone' $pkg/recv_buffer_test.go
  echo "## probe: stall the read on the authored helper's stream"
  cp "$here/c1_1e8d9a91_probe_test.go.txt" $pkg/verify_c1_probe_test.go
  run go test -count=1 -timeout 60s -v -run '^TestVerifyC1_' ./$pkg
  rm -f $pkg/verify_c1_probe_test.go
  echo "## fault injection: recvBuffer.put loses the terminal error and everything after it (end-of-stream never delivered), authored test run unmodified"
  sed -i 's|^func (b \*recvBuffer) put(r recvMsg) {$|&\n\tif os.Getenv("VERIFY_C1_DROP_ERR") != "" {\n\t\tif r.err != nil {\n\t\t\tverifyC1Lost = true\n\t\t}\n\t\tif verifyC1Lost {\n\t\t\treturn\n\t\t}\n\t}|; s|^func (b \*recvBuffer) init() {$|var verifyC1Lost bool\n\n&|' $pkg/transport.go
  grep -q '^	"os"$' $pkg/transport.go || sed -i '0,/^import (/s//import (\n\t"os"/' $pkg/transport.go
  git diff -- $pkg/transport.go | cat
  run go test -count=1 -timeout 60s -run '^Test$/^RecvBuffer_DataDeliveredInOrder$' ./$pkg
  echo "(next command's output is filtered to the test progress lines, the timeout panic and the authored frames of the blocked goroutine)"
  { run VERIFY_C1_DROP_ERR=1 go test -count=1 -timeout 30s -v -run '^Test$/^RecvBuffer_DataDeliveredInOrder$' ./$pkg 2>&1 ; } 2>&1 | grep '^+ \|RUN\|panic: test timed out\|running tests\|recv_buffer_test.go\|recvBufferReader).read\|^--- \|^FAIL\|^ok\|^real' | head -30
  ;;
8c775707|786c4c95|0973d83c)
  case $hash in
    8c775707) f=$pkg/recv_buffer_compaction_test.go; tst='ClientTransport_TinyDataFramesReceiveMemory';;
    786c4c95) f=$pkg/transport_test.go; tst='ClientStream_ManyTinyDataFrames';;
    0973d83c) f=$pkg/recv_buffer_compaction_test.go; tst='ClientRecvBufferCompaction_TinyDataFrames';;
  esac
  echo "## authored blocking socket operations and every deadline/timeout/close near them in $f"
  grep -n 'lis.Accept()\|SetDeadline\|SetReadDeadline\|SetWriteDeadline\|lis.Close()\|conn.Close()\|WithTimeout\|io.ReadFull(\|ReadFrame()' $f | awk -F: -v s="$(grep -n 'lis.Accept()' $f | tail -1 | cut -d: -f1)" '$1 > s-140 && $1 < s+140'
  echo "## control: authored test, unmodified tree"
  run go test -count=1 -timeout 120s -run "^Test\$/^${tst}\$" ./$pkg
  echo "## fault injection: client never connects, authored test run unmodified, watchdog reports goroutines parked in Accept"
  stall_dial; watch
  run VERIFY_C1_STALL_DIAL=1 VERIFY_C1_WATCH='\.Accept\(' go test -count=1 -timeout 120s -v -run "^Test\$/^${tst}\$" ./$pkg
  ;;
e11b3799)
  echo "## authored fixture (all added in this branch: $(git diff HEAD~1 HEAD -- $pkg/transport_test.go | grep -c '^+.*tinyFrameServer') added lines mention tinyFrameServer)"; grep -n 'func (ts \*tinyFrameServer) stop' -A4 $pkg/transport_test.go; grep -n 'ts.done\|newTinyFrameServer(t' -A1 $pkg/transport_test.go
  echo "## control: authored test, unmodified tree"
  run go test -count=1 -timeout 120s -run '^Test$/^ClientReceiveMemory_TinyDataFrames$' ./$pkg
  echo "## probe: stall the goroutine joined by the authored stop()"
  cp "$here/c1_e11b3799_probe_test.go.txt" $pkg/verify_c1_probe_test.go
  run go test -count=1 -timeout 60s -v -run '^TestVerifyC1_' ./$pkg
  rm -f $pkg/verify_c1_probe_test.go
  echo "## fault injection: client never connects, authored test run unmodified, watchdog reports goroutines parked in Accept or stop()"
  stall_dial; watch
  run VERIFY_C1_STALL_DIAL=1 VERIFY_C1_WATCH='\.Accept\(|tinyFrameServer\)\.stop' go test -count=1 -timeout 120s -v -run '^Test$/^ClientReceiveMemory_TinyDataFrames$' ./$pkg
  ;;
*) echo "unknown branch $hash"; exit 2;;
esac
