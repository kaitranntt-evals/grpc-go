// Isolates the audit repros from the grpc module so `go build ./...` / `go vet ./...` at the repo root skip them.
module verifyrepro

go 1.25
