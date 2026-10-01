#!/usr/bin/env python3
# How to run: python3 verify/probes/c6/c6_apply.py <worktree> <6f20f646|3378298f|3acb1605>   (then run the branch's own tests with go test -v; see verify/evidence.md#c6)
# Test-file-only instrumentation for C6: logs what the Stream.readTo helper frees and swaps mem.DefaultBufferPool() for a counting pool in the branch's pooled tests.
import sys, shutil, os, re
wt, br = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
T = os.path.join(wt, 'internal/transport')
shutil.copy(os.path.join(here, 'verify_c6_helpers_test.go'), os.path.join(T, 'verify_c6_helpers_test.go'))

def patch(path, pairs):
    s = open(path).read()
    for a, b, n in pairs:
        assert s.count(a) >= 1, (path, a)
        s = s.replace(a, b) if n == 0 else s.replace(a, b, n)
    open(path, 'w').write(s)

# The pre-existing reader helper: keep its `defer data.Free()` and log around it.
patch(os.path.join(T, 'transport_test.go'), [('''	data, err := s.read(len(p))
	defer data.Free()
''', '''	data, err := s.read(len(p))
	defer verifyC6AfterFree(verifyC6BeforeFree(data)) // VERIFY: runs after the Free below
	defer data.Free()
''', 1)])

if br == '6f20f646':
    patch(os.path.join(T, 'recv_buffer_compaction_test.go'), [('BufferPool:            mem.DefaultBufferPool(),', 'BufferPool:            verifyC6Pool,', 1)])
elif br == '3378298f':
    patch(os.path.join(T, 'recv_buffer_test.go'), [
        ('''	s.buf.init(nil)
	s.trReader = transportReader{''', '''	s.buf.init(verifyC6Pool) // VERIFY: was init(nil), which selects mem.DefaultBufferPool()
	s.trReader = transportReader{''', 1),
        ('&ServerConfig{BufferPool: mem.DefaultBufferPool()}', '&ServerConfig{BufferPool: verifyC6Pool}', 1)])
elif br == '3acb1605':
    patch(os.path.join(T, 'transport_test.go'), [
        ('''	st := newTestRecvStream()
	pool := mem.DefaultBufferPool()''', '''	st := newTestRecvStream()
	var pool mem.BufferPool = verifyC6Pool // VERIFY: was mem.DefaultBufferPool()''', 1),
        ('''			server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)
			defer server.stop()

			conn, err := net.Dial("tcp", server.lis.Addr().String())
			if err != nil {
				t.Fatalf("Client failed to dial: %v", err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(''', '''			server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: verifyC6Pool}, suspended)
			defer server.stop()

			conn, err := net.Dial("tcp", server.lis.Addr().String())
			if err != nil {
				t.Fatalf("Client failed to dial: %v", err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(''', 1)])
else:
    sys.exit('unknown branch')
