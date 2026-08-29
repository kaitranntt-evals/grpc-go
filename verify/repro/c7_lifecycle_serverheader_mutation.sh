#!/usr/bin/env bash
# C7 repro: from the audited repo root (with the eval fixtures copied in place) run `bash verify/repro/c7_lifecycle_serverheader_mutation.sh` — part A runs the lifecycle group on the implementation that omits the empty unary ServerHeader binlog event (see C6); part B additionally injects a synthetic empty ServerHeader on streaming send failure; both runs stay green.
set -euo pipefail
echo "== Part A: lifecycle group on audited implementation (unary ServerHeader omitted on failed send) =="
bash ./test/run_eval_server_test_group.sh lifecycle
echo "== Part B: mutate serverStream.SendMsg to log a synthetic empty ServerHeader on streaming send failure =="
cp stream.go stream.go.bak
trap 'mv stream.go.bak stream.go' EXIT
python3 - <<'EOF'
old = """		if err != nil && err != io.EOF {
			st, _ := status.FromError(toRPCErr(err))
			ss.s.WriteStatus(st)"""
new = """		if err != nil && err != io.EOF {
			if !ss.isUnary && len(ss.binlogs) != 0 && !ss.serverHeaderBinlogged {
				sh := &binarylog.ServerHeader{}
				ss.serverHeaderBinlogged = true
				for _, binlog := range ss.binlogs {
					binlog.Log(ss.ctx, sh)
				}
			}
			st, _ := status.FromError(toRPCErr(err))
			ss.s.WriteStatus(st)"""
s = open('stream.go').read()
i = s.index('func (ss *serverStream) SendMsg')
assert old in s[i:]
open('stream.go', 'w').write(s[:i] + s[i:].replace(old, new, 1))
EOF
bash ./test/run_eval_server_test_group.sh lifecycle
echo "RESULT: lifecycle group green with both ServerHeader alterations"
