#!/usr/bin/env bash
# Run: from the repo root, `verify/repro/run.sh <c1|c2|c2-mutant|c3|c4|c5|c5-mutant>`; replays the audit evidence for that claim in a throwaway worktree of the claim's target branch.
set -uo pipefail
REPO_ROOT="$(git rev-parse --show-toplevel)"
REPRO="$REPO_ROOT/verify/repro"
CLAIM_REPO="${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking}"
BASE=bf9e7cd3430df40d0732ba42eb88bd5f2cc63407
declare -A BRANCH=([c1]=evalon/grpc-go-en-f7299717 [c2]=evalon/grpc-go-en-b139ba1f [c3]=evalon/grpc-go-en-2a7c48e0 [c4]=evalon/grpc-go-en-78149e07 [c5]=evalon/grpc-go-en-33ac8ac2)
what="${1:?usage: run.sh <c1|c2|c2-mutant|c3|c4|c5|c5-mutant>}"
claim="${what%%-*}"
wt="$(mktemp -d)/wt"
git -C "$REPO_ROOT" fetch -q "$CLAIM_REPO" "${BRANCH[$claim]}"
git -C "$REPO_ROOT" worktree add -q --detach "$wt" FETCH_HEAD
trap 'git -C "$REPO_ROOT" worktree remove --force "$wt"' EXIT
cd "$wt"
es=balancer/endpointsharding
case "$what" in
c1)
	files=$(git diff --name-only "$BASE" HEAD -- balancer/endpointsharding | grep '\.go$')
	echo "$files"
	gofmt -d $files; echo "gofmt -d rc=$?"
	gofmt -s -d -l balancer/endpointsharding; echo "gofmt -s -d -l rc=$?"
	;;
c2)
	cp "$REPRO/c2_removed_child_exitidle_test.go.txt" $es/c2_removed_child_exitidle_test.go
	go test ./$es -run 'Test/ReproC2' -race -count=20 -v 2>&1 | grep -E 'REPRO|--- (PASS|FAIL): Test/|^(ok|FAIL|PASS)' | sort | uniq -c
	;;
c2-mutant)
	git apply "$REPRO/c2_mutation.patch"
	for i in $(seq 1 40); do
		out=$(go test ./$es -run 'Test/ChildStateExitIdle_AfterChildRemoved' -race -count=1 -v 2>&1)
		echo "mutant_lines=$(grep -c MUTANT <<<"$out") $(grep -E -- '--- (PASS|FAIL): Test/' <<<"$out")"
	done | sed 's/ ([0-9.]*s)//' | sort | uniq -c
	;;
c3)
	cp "$REPRO/c3_setuptest_close_instrumentation_test.go.txt" balancer/ringhash/zz_c3_setuptest_close_instrumentation_test.go
	go test ./balancer/ringhash -run '^TestReproC3_' -race -count=1 -v 2>&1 | grep -E 'C3-INSTR|UNCLOSED|^--- |^(ok|FAIL|PASS)|UpdateClientConnState returned'
	;;
c4)
	cp "$REPRO/c4_resolver_error_early_publish_test.go.txt" $es/c4_resolver_error_early_publish_test.go
	cp "$REPRO/c4_suppression_scope_internal_test.go.txt" $es/c4_suppression_scope_internal_test.go
	go test ./$es -run '^TestReproC4' -race -count=1 -v
	;;
c5)
	cp "$REPRO/c5_stale_update_barrier_test.go.txt" $es/c5_stale_update_barrier_test.go
	go test ./$es -run 'Test/ReproC5' -race -count=20 -v 2>&1 | grep -E 'TRACE|--- (PASS|FAIL): Test/|^(ok|FAIL|PASS)' | sort | uniq -c
	;;
c5-mutant)
	git apply "$REPRO/c5_mutation_global_lock.patch"
	go test -c -race -o "$wt/c5mut.test" ./$es
	seq 1 48 | xargs -P 8 -I{} sh -c "'$wt/c5mut.test' -test.run 'Test/ChildStateExitIdle_NotBlockedByOtherChildUpdate' -test.count=1 -test.v -test.timeout=25s 2>&1 | grep -E -- '--- PASS: Test/|Timeout waiting|panic: test timed out' | sed 's/ ([0-9.]*s)//'" | sort | uniq -c
	;;
*) echo "unknown: $what"; exit 2 ;;
esac
