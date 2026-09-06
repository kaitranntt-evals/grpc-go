/*
 *
 * Copyright 2026 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package test

import (
	"context"
	"testing"

	"google.golang.org/grpc/internal/stubserver"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestUnaryPipelineRoundTrip(t *testing.T) {
	ss := &stubserver.StubServer{
		UnaryCallF: func(_ context.Context, req *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: req.GetPayload()}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer ss.Stop()

	want := "unified unary pipeline"
	resp, err := ss.Client.UnaryCall(context.Background(), &testpb.SimpleRequest{
		Payload: &testpb.Payload{Body: []byte(want)},
	})
	if err != nil {
		t.Fatalf("UnaryCall() failed: %v", err)
	}
	if got := string(resp.GetPayload().GetBody()); got != want {
		t.Fatalf("UnaryCall() payload = %q, want %q", got, want)
	}
}
