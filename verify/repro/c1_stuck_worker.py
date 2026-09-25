#!/usr/bin/env python3
"""C1 repro: cleanup of the branch-added blocked-child concurrency tests.

Each listed branch of kaitranntt-evals/grpc-go-endpointsharding-decouple-locking
adds a test in which a fake child blocks inside UpdateClientConnState on a
release channel while the test drives ExitIdle for another child. The test has
its own "timed out waiting for the configuration update" assertion for the
case where the worker never returns (a child/balancer deadlock, which is the
scenario such a test exists to catch).

This script makes that worker permanently non-returning by injecting
`select {}` right after the child's `<-release` receive, runs the branch's own
focused test with `go test -timeout 40s -race`, and reports whether the test's
cleanup completed on its own (bounded) or hung until the go test runner
panicked (unbounded: the cleanup calls Close()/wg.Wait() while the stuck worker
still holds the child's lock). The mutation is reverted afterwards.

Usage:
    WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py [branch-id ...]

WT_ROOT must contain one checkout per branch, named by the 8-char id
(e.g. ~/wt/2c006c7f is a checkout of evalon/grpc-go-en-2c006c7f).
"""
import os
import re
import subprocess
import sys
import time

# branch-id: (test file, release-channel variable received by the child, -run pattern)
CFG = {
    '2c006c7f': ('balancer/endpointsharding/endpointsharding_children_ext_test.go', 'unblock', 'Test/EndpointSharding_ExitIdleNotBlockedByOtherChildUpdate/ChildState'),
    'e97b83bb': ('balancer/endpointsharding/endpointsharding_ext_test.go', 'unblock', 'Test/EndpointShardingExitIdleWhileOtherChildBlocked'),
    'e6d88a99': ('balancer/endpointsharding/endpointsharding_child_test.go', 'unblockCh', 'Test/EndpointSharding_ExitIdleWhileOtherChildBlocked'),
    '2de785ca': ('balancer/endpointsharding/endpointsharding_sync_ext_test.go', 'releaseB', 'Test/EndpointShardingExitIdleWhileOtherChildBlocked'),
    '926a7310': ('balancer/endpointsharding/concurrency_test.go', 'release', 'Test/ExitIdleWhileAnotherChildUpdates/autoReconnect=false'),
    'b47575cf': ('balancer/endpointsharding/endpointsharding_concurrency_test.go', 'releaseUpdate', 'Test/ExitIdleWhileOtherChildUpdating/autoReconnect=false'),
    'be821e06': ('balancer/endpointsharding/concurrency_ext_test.go', 'release', 'Test/ChildExitIdleDuringOtherChildUpdate'),
    '1ef76c03': ('balancer/endpointsharding/endpointsharding_concurrency_test.go', 'unblock', 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false'),
    '587893f7': ('balancer/endpointsharding/concurrency_test.go', 'releaseUpdate', 'Test/ExitIdleDuringOtherChildUpdate/existing=true'),
    'b1944cc5': ('balancer/endpointsharding/endpointsharding_concurrency_test.go', 'unblockUpdate', 'Test/ExitIdleWhileOtherChildUpdating/autoReconnect=false'),
    'ba10640a': ('balancer/endpointsharding/endpointsharding_concurrency_test.go', 'release', 'Test/ChildExitIdleIndependentOfOtherChildUpdate/autoReconnect=false'),
    '29bcf6a8': ('balancer/endpointsharding/endpointsharding_concurrency_test.go', 'releaseUpdate', 'Test/ChildExitIdleDuringOtherChildUpdate/autoReconnect=false'),
}

TIMEOUT = os.environ.get('GO_TEST_TIMEOUT', '40s')
WT_ROOT = os.path.expanduser(os.environ.get('WT_ROOT', '~/wt'))
LOG_DIR = os.path.expanduser(os.environ.get('C1_LOG_DIR', '~/c1_logs'))
KEY = re.compile(r'^(--- |    --- |FAIL|ok|panic)|[Tt]imed? ?out|goroutine leak|Leaked goroutine')


def run(branch):
    test_file, release_var, run_pattern = CFG[branch]
    wt = os.path.join(WT_ROOT, branch)
    path = os.path.join(wt, test_file)
    original = open(path).read()
    m = re.compile(r'^(\s*)<-' + re.escape(release_var) + r'\n', re.M).search(original)
    if not m:
        raise SystemExit(f'{branch}: could not find "<-{release_var}" in {test_file}')
    injected = m.group(1) + 'select {} // C1 repro: worker never returns (simulated child/balancer deadlock)\n'
    open(path, 'w').write(original[:m.end()] + injected + original[m.end():])
    try:
        print(f'===== {branch} {test_file} -run {run_pattern}', flush=True)
        subprocess.run(['git', 'diff', '--no-color', '--', test_file], cwd=wt)
        t0 = time.time()
        p = subprocess.run(['go', 'test', './balancer/endpointsharding', '-run', run_pattern, '-race', '-count=1', '-v', '-timeout', TIMEOUT],
                           cwd=wt, capture_output=True, text=True)
        wall = time.time() - t0
        os.makedirs(LOG_DIR, exist_ok=True)
        log = os.path.join(LOG_DIR, f'{branch}.stuck.log')
        open(log, 'w').write(p.stdout + p.stderr)
        for line in (p.stdout + p.stderr).splitlines():
            if KEY.search(line):
                print(line)
        verdict = 'UNBOUNDED (hung until go test -timeout)' if 'test timed out after' in p.stdout + p.stderr else 'bounded (test cleanup completed on its own)'
        print(f'exit={p.returncode} wall={wall:.1f}s cleanup={verdict} log={log}', flush=True)
    finally:
        open(path, 'w').write(original)


if __name__ == '__main__':
    for b in (sys.argv[1:] or list(CFG)):
        run(b)
