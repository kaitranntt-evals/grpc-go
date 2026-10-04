# Audit C2 positive control: make UpdateClientConnState hold es.mu across the
# call into an existing child, so the lock checker has something to catch.
import sys
p = sys.argv[1]
s = open(p).read()
a = "\t\t\tchildBalancer.childState.Endpoint = endpoint\n\t\t\tes.mu.Unlock()\n"
assert s.count(a) == 1
s = s.replace(a, "\t\t\tchildBalancer.childState.Endpoint = endpoint\n")
a = "\t\tnewChildren.Set(endpoint, childBalancer)\n\t\tif err := childBalancer.updateClientConnState("
assert s.count(a) == 1
s = s.replace(a, "\t\tnewChildren.Set(endpoint, childBalancer)\n\t\tif ok {\n\t\t\tdefer es.mu.Unlock()\n\t\t}\n\t\tif err := childBalancer.updateClientConnState(")
open(p, "w").write(s)
