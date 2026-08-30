// Audit probe for claim C9 on branch evalon/grpc-go-se-8b49f661.
// Run: go test . -run TestAuditC9 -count=1 -v
package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const c9Timeout = 20 * time.Second

// ---- grpclog capture ----

type c9LogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *c9LogCapture) write(args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintln(&c.buf, args...)
}
func (c *c9LogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
func (c *c9LogCapture) Info(args ...any)            { c.write(args...) }
func (c *c9LogCapture) Infoln(args ...any)          { c.write(args...) }
func (c *c9LogCapture) Infof(f string, a ...any)    { c.write(fmt.Sprintf(f, a...)) }
func (c *c9LogCapture) Warning(args ...any)         { c.write(args...) }
func (c *c9LogCapture) Warningln(args ...any)       { c.write(args...) }
func (c *c9LogCapture) Warningf(f string, a ...any) { c.write(fmt.Sprintf(f, a...)) }
func (c *c9LogCapture) Error(args ...any)           { c.write(args...) }
func (c *c9LogCapture) Errorln(args ...any)         { c.write(args...) }
func (c *c9LogCapture) Errorf(f string, a ...any)   { c.write(fmt.Sprintf(f, a...)) }
func (c *c9LogCapture) Fatal(args ...any)           { c.write(args...) }
func (c *c9LogCapture) Fatalln(args ...any)         { c.write(args...) }
func (c *c9LogCapture) Fatalf(f string, a ...any)   { c.write(fmt.Sprintf(f, a...)) }
func (c *c9LogCapture) V(int) bool                  { return true }

// ---- server codec that can fail or return an oversized (>4GiB) response ----

type c9Codec struct{}

func (c9Codec) Name() string { return "proto" }
func (c9Codec) Marshal(v any) ([]byte, error) {
	if sv, ok := v.(*wrapperspb.StringValue); ok {
		switch sv.GetValue() {
		case "marshalfail":
			return nil, errors.New("c9: induced marshal failure")
		case "oversize":
			return make([]byte, 1<<32), nil // > math.MaxUint32-1 bytes triggers "message too large"
		}
	}
	return proto.Marshal(v.(proto.Message))
}
func (c9Codec) Unmarshal(data []byte, v any) error {
	return proto.Unmarshal(data, v.(proto.Message))
}

// ---- compressor whose Compress fails ----

type c9FailComp struct{}

func (c9FailComp) Name() string                                { return "c9failcomp" }
func (c9FailComp) Compress(w io.Writer) (io.WriteCloser, error) { return c9FailWriter{}, nil }
func (c9FailComp) Decompress(r io.Reader) (io.Reader, error)   { return r, nil }

type c9FailWriter struct{}

func (c9FailWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		return 0, errors.New("c9: induced compression failure")
	}
	return 0, nil
}
func (c9FailWriter) Close() error { return nil }

func init() { encoding.RegisterCompressor(c9FailComp{}) }

func TestAuditC9_SendMsgClassification(t *testing.T) {
	capture := &c9LogCapture{}
	grpclog.SetLoggerV2(capture)

	handler := func(reply string) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
		return func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := new(emptypb.Empty)
			if err := dec(in); err != nil {
				return nil, err
			}
			return wrapperspb.String(reply), nil
		}
	}
	desc := &grpc.ServiceDesc{
		ServiceName: "audit.C9",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "MarshalFail", Handler: handler("marshalfail")},
			{MethodName: "Oversize", Handler: handler("oversize")},
			{MethodName: "CompressFail", Handler: handler("payload-to-compress")},
		},
	}

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := grpc.NewServer(grpc.ForceServerCodec(c9Codec{}))
	srv.RegisterService(desc, struct{}{})
	go srv.Serve(lis)
	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() {
		cc.Close()
		srv.Stop()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c9Timeout)
	defer cancel()

	run := func(name, method string, opts ...grpc.CallOption) (rpcErr error, delta string) {
		before := capture.String()
		rpcErr = cc.Invoke(ctx, method, &emptypb.Empty{}, &wrapperspb.StringValue{}, opts...)
		time.Sleep(300 * time.Millisecond)
		delta = strings.TrimPrefix(capture.String(), before)
		t.Logf("%s: rpc error = %v", name, rpcErr)
		t.Logf("%s: status message = %q", name, status.Convert(rpcErr).Message())
		t.Logf("%s: server log delta = %q", name, delta)
		return rpcErr, delta
	}

	// (a) marshal failure: text prefix "grpc: error while marshaling:"
	_, d1 := run("marshal-fail", "/audit.C9/MarshalFail")
	// (b) compression failure: text prefix "grpc: error while compressing:"
	_, d2 := run("compress-fail", "/audit.C9/CompressFail", grpc.UseCompressor("c9failcomp"))
	// (c) same provenance (prepareMsg/encode) but different text: "grpc: message too large"
	_, d3 := run("oversize", "/audit.C9/Oversize")

	if strings.Contains(d1, "failed to encode response") &&
		strings.Contains(d2, "failed to compress response") &&
		!strings.Contains(d3, "failed to") {
		t.Logf("CONCLUSION: classification tracks the human-readable status text prefix; the equally real preparation failure with different text (oversize) produced no stage diagnostic")
	} else {
		t.Errorf("unexpected classification pattern: d1=%q d2=%q d3=%q", d1, d2, d3)
	}
}
