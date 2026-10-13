// Run: companion helper for c1_c2_partial_failure_test.go on branch evalon/grpc-go-xd-73a502c4; copy both into internal/xds/server/ and run: go test -tags verify_repro -race -count=1 -v -run 'Test/VerifyPartial' ./internal/xds/server

//go:build verify_repro

package server

// publishVerifyURC publishes urc the way this branch's handleRDSUpdate does.
func publishVerifyURC(fc *filterChain, urc *usableRouteConfiguration) {
	fc.updateUsableRouteConfiguration(urc)
}
