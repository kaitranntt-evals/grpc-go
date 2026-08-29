#!/usr/bin/env bash
# C9 repro: run `bash c9_payload_weakening.sh <checkout-of-evalon/grpc-go-se-4c4409b0>` — shows the weakened payload{} expectation passes (and the provided checks accept it) while restoring the original explicit empty byte-payload assertion fails.
set -euo pipefail
cd "$1"
echo "== the branch's weakening =="
git diff 0c51461d27177d997e14c642fe18c11668fc09a3 HEAD -- gcp/observability/logging_test.go
echo "== weakened assertion passes =="
(cd gcp/observability && go test . -run '^Test$/^ServerRPCEventsLogAll$' -count=1)
echo "== provided submodule check accepts it =="
(cd gcp/observability && go test -json . -run '^Test$/^ServerRPCEventsLogAll$' -count=1 | grep -F '"Test":"Test/ServerRPCEventsLogAll"' | grep -F '"Action":"pass"')
echo "== restoring the original explicit empty byte-payload assertion fails =="
f=gcp/observability/logging_test.go
cp "$f" "$f.bak"
trap 'mv "$f.bak" "$f"' EXIT
python3 - <<'EOF'
p = 'gcp/observability/logging_test.go'
s = open(p).read()
s = s.replace("""			SequenceID:  4,
			Payload:     payload{},""", """			SequenceID:  4,
			Payload: payload{
				Message: []uint8{},
			},""", 1)
open(p, 'w').write(s)
EOF
(cd gcp/observability && go test . -run '^Test$/^ServerRPCEventsLogAll$' -count=1) || echo "RESULT: original strict assertion fails on this branch; payload{} masks the altered representation"
