// Run: for i in $(seq 1 20); do go run verify/repro/c1_map_format_probe.go; done | sort -u
// Prints the exact map[string]int values from TestServerUnifiedPipeline_InterceptorSegregation
// (branch evalon/grpc-go-se-e4b8614a) the way the test formats them (fmt.Sprint / %v).
package main

import "fmt"

func main() {
	wantStream := map[string]int{
		"/grpc.testing.TestService/StreamingInputCall":  1,
		"/grpc.testing.TestService/StreamingOutputCall": 1,
		"/grpc.testing.TestService/FullDuplexCall":      1,
	}
	wantUnary := map[string]int{"/grpc.testing.TestService/EmptyCall": 1}
	for i := 0; i < 1000; i++ {
		fmt.Println(fmt.Sprint(wantStream), fmt.Sprint(wantUnary))
	}
}
