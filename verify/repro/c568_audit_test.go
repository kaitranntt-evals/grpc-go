// Audit probes for claims C5, C6, C8 on branch evalon/grpc-go-se-55a5e139.
// Run: go test . -run 'TestAuditC(5|6|8)' -count=1 -v
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
	iblog "google.golang.org/grpc/internal/binarylog"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const auditTimeout = 10 * time.Second

// ---- raw codec so the client can send arbitrary (malformed) bytes ----

type auditRawCodec struct{}

func (auditRawCodec) Marshal(v any) ([]byte, error) { return v.([]byte), nil }
func (auditRawCodec) Unmarshal(data []byte, v any) error {
	*(v.(*[]byte)) = append([]byte(nil), data...)
	return nil
}
func (auditRawCodec) Name() string { return "proto" }

// ---- compressor whose Compress fails on non-empty payloads ----

type auditFailComp struct{}

func (auditFailComp) Name() string { return "auditfailcomp" }
func (auditFailComp) Compress(w io.Writer) (io.WriteCloser, error) {
	return auditFailWriter{}, nil
}
func (auditFailComp) Decompress(r io.Reader) (io.Reader, error) { return r, nil }

type auditFailWriter struct{}

func (auditFailWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		return 0, errors.New("auditfailcomp: induced compression failure")
	}
	return 0, nil
}
func (auditFailWriter) Close() error { return nil }

func init() { encoding.RegisterCompressor(auditFailComp{}) }

// ---- grpclog capture ----

type auditLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *auditLogCapture) write(args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintln(&c.buf, args...)
}
func (c *auditLogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
func (c *auditLogCapture) Info(args ...any)                    { c.write(args...) }
func (c *auditLogCapture) Infoln(args ...any)                  { c.write(args...) }
func (c *auditLogCapture) Infof(f string, a ...any)            { c.write(fmt.Sprintf(f, a...)) }
func (c *auditLogCapture) Warning(args ...any)                 { c.write(args...) }
func (c *auditLogCapture) Warningln(args ...any)               { c.write(args...) }
func (c *auditLogCapture) Warningf(f string, a ...any)         { c.write(fmt.Sprintf(f, a...)) }
func (c *auditLogCapture) Error(args ...any)                   { c.write(args...) }
func (c *auditLogCapture) Errorln(args ...any)                 { c.write(args...) }
func (c *auditLogCapture) Errorf(f string, a ...any)           { c.write(fmt.Sprintf(f, a...)) }
func (c *auditLogCapture) Fatal(args ...any)                   { c.write(args...) }
func (c *auditLogCapture) Fatalln(args ...any)                 { c.write(args...) }
func (c *auditLogCapture) Fatalf(f string, a ...any)           { c.write(fmt.Sprintf(f, a...)) }
func (c *auditLogCapture) V(int) bool                          { return true }

// ---- binarylog capture ----

type auditBinlogLogger struct {
	mu      sync.Mutex
	entries []iblog.LogEntryConfig
}

func (l *auditBinlogLogger) GetMethodLogger(string) iblog.MethodLogger { return (*auditBinlogML)(l) }

type auditBinlogML auditBinlogLogger

func (m *auditBinlogML) Log(_ context.Context, c iblog.LogEntryConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, c)
}

// ---- service under test ----

func auditStartServer(t *testing.T) *grpc.ClientConn {
	t.Helper()
	desc := &grpc.ServiceDesc{
		ServiceName: "audit.C568",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "EmptyUnary",
				Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(emptypb.Empty)
					if err := dec(in); err != nil {
						return nil, err
					}
					return &emptypb.Empty{}, nil
				},
			},
			{
				MethodName: "BigUnary",
				Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(emptypb.Empty)
					if err := dec(in); err != nil {
						return nil, err
					}
					return wrapperspb.String("payload-to-compress"), nil
				},
			},
		},
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "BidiEncodeFail",
				ClientStreams: true,
				ServerStreams: true,
				Handler: func(_ any, stream grpc.ServerStream) error {
					if err := stream.RecvMsg(new(emptypb.Empty)); err != nil {
						return err
					}
					return stream.SendMsg(42) // not a proto message: encoding failure
				},
			},
			{
				StreamName:    "BidiCompressFail",
				ClientStreams: true,
				ServerStreams: true,
				Handler: func(_ any, stream grpc.ServerStream) error {
					if err := stream.RecvMsg(new(emptypb.Empty)); err != nil {
						return err
					}
					return stream.SendMsg(wrapperspb.String("payload-to-compress"))
				},
			},
		},
	}

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := grpc.NewServer()
	srv.RegisterService(desc, struct{}{})
	go srv.Serve(lis)
	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() {
		cc.Close()
		srv.Stop()
	})
	return cc
}

// C5: malformed second message on a unary method: which decode wording?
func TestAuditC5_UnarySecondMessageDecodeWording(t *testing.T) {
	cc := auditStartServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()

	stream, err := cc.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, "/audit.C568/EmptyUnary", grpc.ForceCodec(auditRawCodec{}))
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := stream.SendMsg([]byte{}); err != nil { // valid empty proto message
		t.Fatalf("SendMsg(valid): %v", err)
	}
	if err := stream.SendMsg([]byte{0xFF}); err != nil { // malformed proto bytes
		t.Fatalf("SendMsg(malformed): %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	var out []byte
	err = stream.RecvMsg(&out)
	if err == nil {
		t.Fatalf("RecvMsg succeeded, want decode error")
	}
	msg := status.Convert(err).Message()
	t.Logf("decode error message: %q", msg)
	if !strings.Contains(msg, "failed to unmarshal the received message") {
		t.Errorf("error does not use streaming wording; got %q", msg)
	}
	if strings.Contains(msg, "error unmarshalling request") {
		t.Errorf("error uses unary wording; got %q", msg)
	}
}

// C6: response-preparation failure diagnostics.
func TestAuditC6_SendDiagnostics(t *testing.T) {
	capture := &auditLogCapture{}
	grpclog.SetLoggerV2(capture)

	cc := auditStartServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()

	// (a) unary compression failure
	err := cc.Invoke(ctx, "/audit.C568/BigUnary", &emptypb.Empty{}, &wrapperspb.StringValue{}, grpc.UseCompressor("auditfailcomp"))
	t.Logf("unary compress-fail RPC error: %v", err)
	logs := capture.String()
	if !strings.Contains(logs, "server failed to encode response") || !strings.Contains(logs, "induced compression failure") {
		t.Errorf("unary compression failure not logged as encode failure; logs:\n%s", logs)
	} else {
		t.Logf("unary compression failure logged as: 'grpc: server failed to encode response: ... induced compression failure'")
	}

	// (b) streaming encoding failure: expect NO server diagnostic
	before := capture.String()
	s1, err := cc.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/audit.C568/BidiEncodeFail")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	s1.SendMsg(&emptypb.Empty{})
	s1.CloseSend()
	rerr := s1.RecvMsg(&emptypb.Empty{})
	t.Logf("streaming encode-fail RPC error: %v", rerr)
	time.Sleep(200 * time.Millisecond)
	delta := strings.TrimPrefix(capture.String(), before)
	if strings.Contains(delta, "encode") || strings.Contains(delta, "compress") {
		t.Logf("streaming encode failure produced diagnostics:\n%s", delta)
	} else {
		t.Logf("streaming encode failure produced NO encode/compress diagnostic (delta logs: %q)", delta)
	}

	// (c) streaming compression failure: expect NO server diagnostic
	before = capture.String()
	s2, err := cc.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/audit.C568/BidiCompressFail", grpc.UseCompressor("auditfailcomp"))
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	s2.SendMsg(&emptypb.Empty{})
	s2.CloseSend()
	rerr = s2.RecvMsg(&wrapperspb.StringValue{})
	t.Logf("streaming compress-fail RPC error: %v", rerr)
	time.Sleep(200 * time.Millisecond)
	delta = strings.TrimPrefix(capture.String(), before)
	if strings.Contains(delta, "encode") || strings.Contains(delta, "compress") {
		t.Logf("streaming compress failure produced diagnostics:\n%s", delta)
	} else {
		t.Logf("streaming compress failure produced NO encode/compress diagnostic (delta logs: %q)", delta)
	}
}

// C8: binary log payload for an empty unary server response: nil or non-nil empty?
func TestAuditC8_EmptyUnaryBinlogPayload(t *testing.T) {
	bl := &auditBinlogLogger{}
	iblog.SetLogger(bl)
	defer iblog.SetLogger(nil)

	cc := auditStartServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()

	if err := cc.Invoke(ctx, "/audit.C568/EmptyUnary", &emptypb.Empty{}, &emptypb.Empty{}); err != nil {
		t.Fatalf("EmptyUnary: %v", err)
	}

	bl.mu.Lock()
	defer bl.mu.Unlock()
	found := false
	for _, e := range bl.entries {
		if sm, ok := e.(*iblog.ServerMessage); ok {
			found = true
			b, isBytes := sm.Message.([]byte)
			t.Logf("binarylog ServerMessage payload: %T, isBytes=%v, nil=%v, len=%d", sm.Message, isBytes, b == nil, len(b))
			if !isBytes || b != nil {
				t.Errorf("ServerMessage payload = %#v; claim expects nil []byte", sm.Message)
			}
		}
	}
	if !found {
		t.Fatalf("no ServerMessage binary log entry captured; entries: %d", len(bl.entries))
	}
}
