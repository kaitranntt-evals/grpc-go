# Run: python3 verify/repro/c1/mutate.py <A|A2|B> <short-branch-id>   (from the root of a claim-branch worktree; used by run_c1.sh)
import re, sys

kind, bid = sys.argv[1], sys.argv[2]

def sub(path, old, new, count=1):
    s = open(path).read()
    if old not in s:
        sys.exit("mutate.py: pattern not found in %s: %r" % (path, old))
    s = s.replace(old, new, count)
    open(path, "w").write(s)

def add_time_import(path):
    s = open(path).read()
    if re.search(r'^\t"time"$', s, re.M):
        return
    s = s.replace('import (\n', 'import (\n\t"time"\n', 1)
    open(path, "w").write(s)

if kind == "B":
    # Production: delay between publishing the replacement configuration and
    # closing the retired configuration's interceptors.
    p = "internal/xds/server/filter_chain_manager.go"
    delay = "time.Sleep(100 * time.Millisecond) // VERIFY mutation B: scheduling delay only\n"
    if bid == "8de853fe":
        sub(p, "\tif old := fc.usableRouteConfiguration.Swap(urc); old != urc {\n\t\told.closeInterceptors()\n",
               "\tif old := fc.usableRouteConfiguration.Swap(urc); old != urc {\n\t\t" + delay + "\t\told.closeInterceptors()\n")
    elif bid == "bbe58e09":
        sub(p, "\tif old := fc.usableRouteConfiguration.Swap(urc); old != nil {\n\t\told.close()\n",
               "\tif old := fc.usableRouteConfiguration.Swap(urc); old != nil {\n\t\t" + delay + "\t\told.close()\n")
    else:  # 1ea7dd6e, 8f0663b7, 295929ad
        sub(p, "\tfc.usableRouteConfiguration.Swap(urc).close()\n",
               "\told := fc.usableRouteConfiguration.Swap(urc)\n\t" + delay + "\told.close()\n")
    add_time_import(p)
elif kind == "A":
    # Test double only: Close takes 100ms before it reports itself destroyed.
    p = "test/xds/xds_server_filter_state_retention_test.go"
    sub(p, "func (i *trackingInterceptor) Close() {\n\ti.parent.interceptorsDestroyed.Add(1)\n",
           "func (i *trackingInterceptor) Close() {\n\ttime.Sleep(100 * time.Millisecond) // VERIFY mutation A: slow Close\n\ti.parent.interceptorsDestroyed.Add(1)\n")
    add_time_import(p)
elif kind == "A2":
    # Test double only: every second Close (the last of each two-filter-chain
    # update) takes 100ms before it reports itself destroyed.
    p = "test/xds/xds_server_filter_state_retention_test.go"
    sub(p, "func (i *trackingInterceptor) Close() {\n\ti.parent.interceptorsDestroyed.Add(1)\n",
           "var verifyCloseCalls atomic.Int32\n\nfunc (i *trackingInterceptor) Close() {\n\tif verifyCloseCalls.Add(1)%2 == 0 {\n\t\ttime.Sleep(100 * time.Millisecond) // VERIFY mutation A2: last Close of the update is slow\n\t}\n\ti.parent.interceptorsDestroyed.Add(1)\n")
    add_time_import(p)
else:
    sys.exit("unknown mutation")
