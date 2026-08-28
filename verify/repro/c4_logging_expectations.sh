# Run: bash verify/repro/c4_logging_expectations.sh <path-to-worktree-on-branch-evalon/grpc-go-se-222348fd>
# C4: runs the focused observability binary-log test (part 1, fails: expects []uint8{}
# while unary logging produces nil) and greps the emitted status-write warning text
# plus any test expectations naming Server.processUnaryRPC (part 2, none exist).
set -x
WT="${1:?worktree path}"
cd "$WT/gcp/observability" && go test -run 'Test/^ServerRPCEventsLogAll$' -count=1 . || true
cd "$WT"
grep -rn "failed to write status" server.go
grep -rn "processUnaryRPC failed to write status" test/ gcp/ || echo "no test expectation names Server.processUnaryRPC"
