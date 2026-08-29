//go:build ignore

package test

// C3 repro: copy into the target branch's `test/` directory, delete the `//go:build ignore` line, then run `go test ./test -run '^TestC3CompressionFailureDiagnostic$' -v -count=1`.
import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type c3BadCompressor struct{}

func (c3BadCompressor) Name() string { return "c3badcomp" }
func (c3BadCompressor) Compress(io.Writer) (io.WriteCloser, error) {
	return nil, errors.New("c3 induced compression failure")
}
func (c3BadCompressor) Decompress(r io.Reader) (io.Reader, error) { return r, nil }

type c3LogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *c3LogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *c3LogBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestC3CompressionFailureDiagnostic(t *testing.T) {
	encoding.RegisterCompressor(c3BadCompressor{})
	logs := &c3LogBuf{}
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(logs, logs, logs))

	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: []byte("nonempty")}}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Empty request marshals to zero bytes, so client-side compression is
	// skipped; the server's outbound response compression is what fails.
	client := testgrpc.NewTestServiceClient(ss.CC)
	_, err := client.UnaryCall(ctx, &testpb.SimpleRequest{}, grpc.UseCompressor("c3badcomp"))
	t.Logf("client error: %v", err)
	if status.Code(err) != codes.Internal {
		t.Fatalf("status code = %v, want Internal", status.Code(err))
	}
	if !strings.Contains(err.Error(), "error while compressing") {
		t.Errorf("client error does not mention compressing: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	got := logs.String()
	t.Logf("server logs:\n%s", got)
	if strings.Contains(got, "server failed to encode response") {
		t.Log("RESULT: compression failure emitted the encoding-stage diagnostic 'server failed to encode response'")
	} else {
		t.Log("RESULT: no encoding-stage diagnostic observed for compression failure")
	}
}
