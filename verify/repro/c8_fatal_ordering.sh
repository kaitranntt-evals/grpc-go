# Run: bash c8_fatal_ordering.sh <path-to-worktree-of evalon/grpc-go-se-46230bde> — shows one Fatalf aborts TestUnifiedServerPipeline before later scenarios run.
set -euo pipefail
cd "$1"
grep -c 't.Run(' server_unified_pipeline_test.go || true   # 0 => no subtests
go test -v . -run '^TestUnifiedServerPipeline$' -count=1   # baseline: PASS
# Break only the first (unary) scenario: point the first Invoke at a missing method.
sed -i 's|cc.Invoke(ctx, method("Unary"), wrapperspb.String("request")|cc.Invoke(ctx, method("UnaryMissing"), wrapperspb.String("request")|' server_unified_pipeline_test.go
go test -v . -run '^TestUnifiedServerPipeline$' -count=1 || true
# Output shows only "unary Invoke() failed" — no later streaming/status/compression/interceptor scenario reports.
git checkout -- server_unified_pipeline_test.go
