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
	"bytes"
	"context"
	"testing"

	"google.golang.org/grpc/internal/stubserver"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestUnaryResponsePayload(t *testing.T) {
	want := []byte("unary response payload")
	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{
				Payload: &testpb.Payload{Body: want},
			}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	got, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	if err != nil {
		t.Fatalf("ss.Client.UnaryCall() failed: %v", err)
	}
	if !bytes.Equal(got.GetPayload().GetBody(), want) {
		t.Fatalf("ss.Client.UnaryCall() payload = %q, want %q", got.GetPayload().GetBody(), want)
	}
}
