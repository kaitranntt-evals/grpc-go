// Keeps the audit artifacts in this directory out of the main module, so that
// `go build ./...`, `go vet ./...` and `go test ./...` at the repository root
// ignore them. Every artifact is run by copying it into a checkout of the
// branch it targets; see the first line of each file.
module verify.invalid/grpc-go-audit

go 1.24
