#!/usr/bin/env bash
# Usage: verify/repro/c1_tests_accept_premature_invalidation.sh <short-branch-id>...   (any of the 27 C1 branches, e.g. 95f90b92 b7e8d61f; needs FIXTURES_DIR, see setup_worktree.sh)
# C1 repro. For each branch: (1) runs the branch's own new/changed tests as written with load probes: every
# test that overlaps a validation-root load with replacement/retirement shows that load ENDing with a closure
# error and still PASSes; (2) runs them against the --defer-close reference mutant (replaced provider is closed
# only after the loads already running on it returned): tests that *require* the invalidation FAIL;
# (3) runs the archived hidden fixture as written (FAIL: the branch really invalidates the selected load)
# and against the mutant (PASS).
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd); v=$here/..
out=${LOG_ROOT:-$HOME/eval/logs}
for id in "$@"; do
  wt=$("$here/setup_worktree.sh" "$id") || exit 1
  mkdir -p "$out/$id"
  "$v/probe_branch.sh" "$wt" > "$out/$id/probe.log" 2>&1
  "$v/probe_branch.sh" "$wt" --no-close > "$out/$id/probe_noclose.log" 2>&1
  "$v/probe_branch.sh" "$wt" --defer-close > "$out/$id/probe_defer.log" 2>&1
  "$v/probe_fixture.sh" "$wt" > "$out/$id/probe_fixture.log" 2>&1
  "$v/probe_fixture.sh" "$wt" --defer-close > "$out/$id/probe_fixture_defer.log" 2>&1
  python3 "$v/summarize_probes.py" "$out" "$id"
  echo "  hidden fixture as written : $(grep -E '^--- ' "$out/$id/probe_fixture.log") | selected load: $(grep -o 'END .*' "$out/$id/probe_fixture.log" | head -1 | sed -E 's/hi=\S+ //')"
  echo "  hidden fixture, defer-close: $(grep -E '^--- ' "$out/$id/probe_fixture_defer.log")"
done
