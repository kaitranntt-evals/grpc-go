#!/bin/bash
# Run from the repo root: bash verify/repro/c1/run_c1_stalls.sh [worktree-parent-dir]   (creates one worktree per branch from remote "evalrepo" = github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead, then replays every controlled stall; results land in ./c1-out/)
# Each variant injects a stall or early failure into the branch's own test and runs it with `go test -timeout 40s`
# (90s for 1493d957). "panic: test timed out" == no local completion bound (CONFIRMED); finishing at <=20s == bounded.
set -u
R="$(cd "$(dirname "$0")" && pwd)/run_variant.py"
W=${1:-/home/ubuntu/wt}
REMOTE=evalrepo
git remote get-url $REMOTE >/dev/null 2>&1 || git remote add $REMOTE https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead.git
for b in e008f402 086a937f 5b73028f 0fe051bd 04d52ba7 60f32c9f d67a6723 80ac4b7e eb3cc7a4 99413fdb 2db669b5 1493d957; do
  if [ ! -d "$W/$b" ]; then
    git fetch $REMOTE evalon/grpc-go-tr-$b && git worktree add "$W/$b" FETCH_HEAD
  fi
done
OUT=$(pwd)/c1-out
# e008f402: the terminal error is never enqueued -> reader (recvBufferReader without ctx) blocks forever
python3 $R $W/e008f402 internal/transport/recv_buffer_test.go e008f402_stall_ownership_noTerminalErr 'Test/RecvBufferCompactionOwnershipAndErrors/error=EOF/header=false' 40s \
 '[["\t\t\t\tb.put(recvMsg{buffer: newBuffer('"'"'x'"'"'), err: terminalErr})\n\t\t\t\tb.put(recvMsg{buffer: newBuffer('"'"'y'"'"')})\n\t\t\t\tb.put(recvMsg{err: errors.New(\"later error\")})\n", "\t\t\t\t// injected stall: the terminal error is never enqueued\n"]]' $OUT
# e008f402 (control): producer withholds EOF in the ctx-bound concurrent test -> released by ctx at 10s
python3 $R $W/e008f402 internal/transport/recv_buffer_test.go e008f402_stall_noEOF 'Test/RecvBufferCompactionConcurrentRead' 40s \
 '[["\t\tb.put(recvMsg{err: io.EOF})\n\t}()\n\tr := recvBufferReader", "\t}()\n\tr := recvBufferReader"]]' $OUT
# 086a937f: server never reads (4 KiB socket buffers) -> raw client write/Flush blocks despite the test ctx
python3 $R $W/086a937f internal/transport/recv_buffer_test.go 086a937f_stall_serverNoRead 'Test/ServerStream_ManySmallDataFrames/compaction_enabled' 40s \
 '[["\t\t\t\tst, err := NewServerTransport(conn, &ServerConfig{", "\t\t\t\tconn.(*net.TCPConn).SetReadBuffer(4096)\n\t\t\t\tselect {} // injected stall: server never reads\n\t\t\t\tst, err := NewServerTransport(conn, &ServerConfig{"],
   ["\t\t\tdefer conn.Close()\n\n\t\t\t// Discard frames from the server.", "\t\t\tdefer conn.Close()\n\t\t\tconn.(*net.TCPConn).SetWriteBuffer(4096)\n\n\t\t\t// Discard frames from the server."]]' $OUT
# 60f32c9f: the server stream handler blocks HandleStreams' frame-read loop after HEADERS -> raw client DATA writes block despite the test ctx
python3 $R $W/60f32c9f internal/transport/recv_buffer_compaction_test.go 60f32c9f_stall_handlerBlocksReader 'Test/ServerReceivesManySmallDataFrames/enabled' 40s \
 '[["\t\t\t\tst.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s })", "\t\t\t\tst.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s; select {} }) // injected stall: the handler blocks HandleStreams'"'"' frame-read loop, so the server stops reading the socket after HEADERS"],
   ["\t\t\tdefer conn.Close()\n\t\t\tif _, err := conn.Write(clientPreface); err != nil {", "\t\t\tdefer conn.Close()\n\t\t\tconn.(*net.TCPConn).SetWriteBuffer(4096)\n\t\t\tif _, err := conn.Write(clientPreface); err != nil {"]]' $OUT
# 60f32c9f (control): server never creates the transport -> released by the ctx-bound wait for the stream
python3 $R $W/60f32c9f internal/transport/recv_buffer_compaction_test.go 60f32c9f_stall_serverNoRead 'Test/ServerReceivesManySmallDataFrames/enabled' 40s \
 '[["\t\t\t\tst, err := NewServerTransport(conn, &ServerConfig{BufferPool: mem.DefaultBufferPool(), MaxStreams: math.MaxUint32})", "\t\t\t\tconn.(*net.TCPConn).SetReadBuffer(4096)\n\t\t\t\tselect {} // injected stall: server never reads\n\t\t\t\tst, err := NewServerTransport(conn, &ServerConfig{BufferPool: mem.DefaultBufferPool(), MaxStreams: math.MaxUint32})"],
   ["\t\t\tdefer conn.Close()\n\t\t\tif _, err := conn.Write(clientPreface); err != nil {", "\t\t\tdefer conn.Close()\n\t\t\tconn.(*net.TCPConn).SetWriteBuffer(4096)\n\t\t\tif _, err := conn.Write(clientPreface); err != nil {"]]' $OUT
# 0fe051bd: dial a listener nobody accepts for 2s, then a peer that never reads -> dial completes, deadline-bounded I/O fails with i/o timeout
python3 $R $W/0fe051bd internal/transport/recv_buffer_test.go 0fe051bd_stall_serverNoRead 'Test/ServerReceivesMessageInTinyDataFrames' 40s \
 '[["\tmconn, err := net.Dial(\"tcp\", server.lis.Addr().String())\n\tif err != nil {\n\t\tt.Fatalf(\"Client failed to dial: %v\", err)\n\t}\n\tdefer mconn.Close()\n\tif err := mconn.SetDeadline(", "\tlis, err := net.Listen(\"tcp\", \"localhost:0\") // injected: raw peer that never reads\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tdefer lis.Close()\n\tgo func() {\n\t\ttime.Sleep(2 * time.Second) // injected: nobody calls Accept for 2s\n\t\tc, err := lis.Accept()\n\t\tif err != nil {\n\t\t\treturn\n\t\t}\n\t\tc.(*net.TCPConn).SetReadBuffer(4096)\n\t\tselect {} // injected stall: peer never reads\n\t}()\n\tdialStart := time.Now()\n\tmconn, err := net.Dial(\"tcp\", lis.Addr().String())\n\tt.Logf(\"net.Dial returned after %v (Accept delayed by 2s)\", time.Since(dialStart))\n\tif err != nil {\n\t\tt.Fatalf(\"Client failed to dial: %v\", err)\n\t}\n\tdefer mconn.Close()\n\tmconn.(*net.TCPConn).SetWriteBuffer(4096)\n\tif err := mconn.SetDeadline("]]' $OUT
# 04d52ba7: same stall for its raw-client server test
python3 $R $W/04d52ba7 internal/transport/recv_buffer_compaction_test.go 04d52ba7_stall_serverNoRead 'Test/ServerReceivesManySmallDataFrames' 40s \
 '[["\tmconn, err := net.Dial(\"tcp\", server.lis.Addr().String())\n\tif err != nil {\n\t\tt.Fatalf(\"Client failed to dial: %v\", err)\n\t}\n\tdefer mconn.Close()\n\tif err := mconn.SetWriteDeadline(", "\tlis, err := net.Listen(\"tcp\", \"localhost:0\") // injected: raw peer that never reads\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tdefer lis.Close()\n\tgo func() {\n\t\ttime.Sleep(2 * time.Second) // injected: nobody calls Accept for 2s\n\t\tc, err := lis.Accept()\n\t\tif err != nil {\n\t\t\treturn\n\t\t}\n\t\tc.(*net.TCPConn).SetReadBuffer(4096)\n\t\tselect {} // injected stall: peer never reads\n\t}()\n\tdialStart := time.Now()\n\tmconn, err := net.Dial(\"tcp\", lis.Addr().String())\n\tt.Logf(\"net.Dial returned after %v (Accept delayed by 2s)\", time.Since(dialStart))\n\tif err != nil {\n\t\tt.Fatalf(\"Client failed to dial: %v\", err)\n\t}\n\tdefer mconn.Close()\n\tmconn.(*net.TCPConn).SetWriteBuffer(4096)\n\tif err := mconn.SetWriteDeadline("]]' $OUT
# d67a6723: early failure before the client transport exists -> deferred <-serverDone waits on a goroutine stuck in lis.Accept()
python3 $R $W/d67a6723 internal/transport/recv_buffer_compaction_test.go d67a6723_earlyfail_beforeClient 'Test/ClientReceivesManySmallDataFrames' 40s \
 '[["\tct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})\n\tif err != nil {\n\t\tt.Fatalf(\"NewHTTP2Client() failed: %v\", err)", "\tif true {\n\t\tt.Fatalf(\"injected early failure before the client transport is created\")\n\t}\n\tct, err := NewHTTP2Client(ctx, ctx, resolver.Address{Addr: lis.Addr().String()}, ConnectOptions{BufferPool: mem.DefaultBufferPool()}, func(GoAwayInfo) {})\n\tif err != nil {\n\t\tt.Fatalf(\"NewHTTP2Client() failed: %v\", err)"]]' $OUT
# 5b73028f: early failure while the server writer goroutine is still writing -> writer exits via connection reset
python3 $R $W/5b73028f internal/transport/recv_buffer_test.go 5b73028f_earlyfail_whileWriting 'Test/ClientStream_ManySmallDataFrames' 40s \
 '[["\t\t// Read only after all frames have been received and queued.\n\t\tselect {", "\t\tt.Fatalf(\"injected early failure while the server writer is still running\")\n\t\t// Read only after all frames have been received and queued.\n\t\tselect {"]]' $OUT
python3 $R $W/5b73028f internal/transport/recv_buffer_test.go 5b73028f_earlyfail_afterDone 'Test/ClientStream_ManySmallDataFrames' 40s \
 '[["\t\theader := make([]byte, 5)\n\t\tif err := stream.ReadMessageHeader(header); err != nil {\n\t\t\tt.Fatalf(\"stream.ReadMessageHeader() failed: %v\", err)", "\t\tif true {\n\t\t\tt.Fatalf(\"injected early failure after the stream finished\")\n\t\t}\n\t\theader := make([]byte, 5)\n\t\tif err := stream.ReadMessageHeader(header); err != nil {\n\t\t\tt.Fatalf(\"stream.ReadMessageHeader() failed: %v\", err)"]]' $OUT
# 80ac4b7e / 99413fdb / 2db669b5 / eb3cc7a4: producer withholds EOF in the concurrent tests that join with `defer func() { <-done }()`
python3 $R $W/80ac4b7e internal/transport/recv_buffer_test.go 80ac4b7e_stall_noEOF 'Test/ReceiveBufferCompactionConcurrent$' 40s \
 '[["\t\tqueue.put(recvMsg{err: io.EOF})\n\t}()\n\tdefer func() { <-done }()", "\t}()\n\tdefer func() { <-done }()"]]' $OUT
python3 $R $W/99413fdb internal/transport/recv_buffer_test.go 99413fdb_stall_noEOF 'Test/ReceiveBufferCompactionConcurrentReads' 40s \
 '[["\t\tqueue.put(recvMsg{err: io.EOF})\n\t}()\n\tdefer func() { <-done }()", "\t}()\n\tdefer func() { <-done }()"]]' $OUT
python3 $R $W/2db669b5 internal/transport/recv_buffer_test.go 2db669b5_stall_noEOF_concurrent 'Test/ReceiveBufferCompactionConcurrentRead' 40s \
 '[["\t\trecv.put(recvMsg{err: io.EOF})\n\t}()\n\tdefer func() { <-done }()", "\t}()\n\tdefer func() { <-done }()"]]' $OUT
python3 $R $W/eb3cc7a4 internal/transport/recv_buffer_test.go eb3cc7a4_stall_noEOF_concurrent 'Test/RecvBufferCompactionConcurrentRead' 40s \
 '[["\t\tqueue.put(recvMsg{err: io.EOF})\n\t}()\n\tdefer func() { <-done }()", "\t}()\n\tdefer func() { <-done }()"]]' $OUT
# eb3cc7a4 ownership: terminal error never queued -> reader without ctx blocks forever
python3 $R $W/eb3cc7a4 internal/transport/recv_buffer_test.go eb3cc7a4_stall_ownership_noErr 'Test/RecvBufferCompactionOwnership' 40s \
 '[["\t\t\tput([]byte{103}, terminalErr)", "\t\t\tput([]byte{103}, nil) // injected stall: terminal error never queued"]]' $OUT
# 2db669b5 no-recopy: EOF never queued -> bare `<-recv.get()` blocks forever
python3 $R $W/2db669b5 internal/transport/recv_buffer_test.go 2db669b5_stall_norecopy_noEOF 'Test/ReceiveBufferCompactionNoRecopy' 40s \
 '[["\trecv.put(recvMsg{err: io.EOF})\n\tvar got []byte\n\tfor {\n\t\tmsg := <-recv.get()", "\tvar got []byte // injected stall: EOF never queued\n\tfor {\n\t\tmsg := <-recv.get()"]]' $OUT
# 1493d957 (C10): server never sets END_STREAM -> <-stream.Done() is released when the helper's ctx-bound server goroutine closes the connection
python3 $R $W/1493d957 internal/transport/recv_buffer_test.go 1493d957_stall_noEndStream 'Test/ClientTransport_ManySmallDataFrames/enabled' 90s \
 '[["framer.WriteData(streamID, i == numFrames-1, want[i:i+1]); err != nil {", "framer.WriteData(streamID, false, want[i:i+1]); err != nil { // injected stall: END_STREAM never sent"]]' $OUT
echo ALLDONE
