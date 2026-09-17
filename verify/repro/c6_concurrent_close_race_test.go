//go:build verify_repro

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

// Run (against branch evalon/grpc-go-xd-34a8fdbb in a worktree):
//   cp verify/repro/c6_concurrent_close_race_test.go <worktree>/internal/xds/server/ && (cd <worktree> && go test -race -tags verify_repro ./internal/xds/server/ -run '^Test$/^VerifyC6_ConcurrentConnWrapperClose$' -count=1 -v)
//
// Probe for C6: two goroutines call Close on the same initialized connWrapper.
// Under the race detector, conflicting unsynchronized accesses to
// c.handshakeInfo (read in Release, write of nil) are reported as a DATA RACE.

package server

import (
	"context"
	"crypto/x509"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
)

type verifyC6Provider struct{}

func (verifyC6Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}
func (verifyC6Provider) Close() {}

func (s) TestVerifyC6_ConcurrentConnWrapperClose(t *testing.T) {
	for i := 0; i < 200; i++ {
		c1, c2 := net.Pipe()
		defer c2.Close()
		lw := &listenerWrapper{conns: make(map[*connWrapper]bool)}
		cw := &connWrapper{Conn: c1, parent: lw}
		lw.conns[cw] = true
		cw.handshakeInfo = xdsinternal.NewHandshakeInfo(verifyC6Provider{}, verifyC6Provider{}, nil, false, "", false, false)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				cw.Close()
			}()
		}
		close(start)
		wg.Wait()
	}
}
