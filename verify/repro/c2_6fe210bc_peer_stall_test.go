// Run: cp verify/repro/c2_6fe210bc_peer_stall_test.go /tmp/claims/6fe210bc/internal/transport/ && (cd /tmp/claims/6fe210bc && go test ./internal/transport -run '^Test$/^VerifyC2_PeerWithholdsEndStream$' -count=1 -v -timeout 60s); rm /tmp/claims/6fe210bc/internal/transport/c2_6fe210bc_peer_stall_test.go
package transport

import (
	"bytes"
	"context"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Same flow as the branch's TestClientReceivesManySmallDataFrames (same
// helper, same bare `<-stream.Done()` wait), but the manual server stalls: it
// never sends the final DATA frame carrying END_STREAM. The test reports how
// long the bare wait blocked.
func (s) TestVerifyC2_PeerWithholdsEndStream(t *testing.T) {
	serverFrames := func(_ *testing.T, framer *http2.Framer, streamID uint32) {
		var buf bytes.Buffer
		henc := hpack.NewEncoder(&buf)
		henc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"})
		framer.WriteHeaders(http2.HeadersFrameParam{StreamID: streamID, BlockFragment: buf.Bytes(), EndHeaders: true})
		for i := 0; i < 100; i++ {
			framer.WriteData(streamID, false, []byte{byte(i)})
		}
		// Stall: END_STREAM is never sent.
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	start := time.Now()
	stream, waitForServer := setupRSTStreamOnEOSTest(ctx, t, serverFrames)
	defer waitForServer()
	<-stream.Done() // the branch test's bare wait
	t.Logf("<-stream.Done() returned after %v; ctx.Err()=%v status=%v", time.Since(start).Round(time.Millisecond), ctx.Err(), stream.Status())
}
