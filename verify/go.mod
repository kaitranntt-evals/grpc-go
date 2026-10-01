// Not a buildable module: this file only keeps the audit artifacts under verify/
// (test files meant to be copied into internal/transport of a claim branch)
// out of `go build ./...`, `go vet ./...` and `go test ./...` of the main module.
module google.golang.org/grpc/verify-audit-artifacts

go 1.25
