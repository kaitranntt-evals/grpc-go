# Run: bash verify/repro/c6_registry_probe.sh <path-to-worktree-on-branch-evalon/grpc-go-se-581c1cfb> <path-to-eval_tests/tests>
# C6: temporarily instruments the two separate registry lookup branches in
# Server.handleStream, runs the archived interceptor-segregation fixture, and
# shows unary methods resolve via srv.methods and streaming via srv.streams.
set -x
WT="${1:?worktree path}"
FIX="${2:?fixtures path}"
cd "$WT"
python3 - <<'EOF'
src = open('server.go').read()
src = src.replace("\tif md, ok := srv.methods[method]; ok {\n",
  "\tif md, ok := srv.methods[method]; ok {\n\t\tfmt.Fprintln(os.Stderr, \"C6-PROBE: unary registry lookup hit for\", method)\n", 1)
src = src.replace("\tif sd, ok := srv.streams[method]; ok {\n",
  "\tif sd, ok := srv.streams[method]; ok {\n\t\tfmt.Fprintln(os.Stderr, \"C6-PROBE: stream registry lookup hit for\", method)\n", 1)
open('server.go','w').write(src)
EOF
sed -i '0,/^import (/s//import (\n\t"os"/' server.go
cp "$FIX/eval_interceptor_segregation_test.go" test/
go test ./test -run 'TestEval_InterceptorSegregation' -count=1 -v 2>&1 | grep -E "C6-PROBE|^--- |^ok"
git checkout -- server.go
rm test/eval_interceptor_segregation_test.go
