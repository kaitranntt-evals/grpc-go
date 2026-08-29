//go:build ignore

package grpc

// C1 repro: copy into the target branch's repo root, delete the `//go:build ignore` line, then run `go test -run TestC1Probe -v .`.
import (
	"context"
	"testing"
)

func TestC1Probe(t *testing.T) {
	s := NewServer()
	sd := &ServiceDesc{
		ServiceName: "probe.S",
		HandlerType: (*any)(nil),
		Methods: []MethodDesc{{MethodName: "U", Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor UnaryServerInterceptor) (any, error) {
			return nil, nil
		}}},
		Streams: []StreamDesc{{StreamName: "St", Handler: func(srv any, stream ServerStream) error { return nil }}},
	}
	s.RegisterService(sd, &struct{}{})
	info := s.services["probe.S"]
	var mHandler, sHandler bool
	for name, d := range info.methods {
		hasH := hasHandler(d)
		t.Logf("methods[%q] type=%T handlerPresent=%v", name, d, hasH)
		mHandler = mHandler || hasH
	}
	for name, d := range info.streams {
		hasH := d.Handler != nil
		t.Logf("streams[%q] type=%T handlerPresent=%v", name, d, hasH)
		sHandler = sHandler || hasH
	}
	t.Logf("handler-bearing stores: methods=%v streams=%v", mHandler, sHandler)
	if mHandler && sHandler {
		t.Log("RESULT: TWO handler-bearing descriptor stores retained")
	} else {
		t.Log("RESULT: single handler-bearing store")
	}
}

func hasHandler(d any) bool {
	switch v := d.(type) {
	case *StreamDesc:
		return v.Handler != nil
	case *MethodDesc:
		return v.Handler != nil
	}
	return false
}
