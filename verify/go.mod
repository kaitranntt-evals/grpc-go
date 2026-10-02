// Keeps the audit artifacts under verify/ out of the root module, so `go build ./...` and `go vet ./...` at the
// repository root ignore verify/repro/*_test.go (which only compiles once copied into test/xds).
module verify

go 1.25
