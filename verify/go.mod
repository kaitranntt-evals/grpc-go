// Keeps verify/ out of the grpc module so that the repro *_test.go files here (which only compile
// once copied into a claim worktree) are not picked up by `go build ./...` or `go vet ./...`.
module verify

go 1.24
