package grpc

import "fmt"

func _scratchUnreachable() {
	return
	fmt.Println("unreachable code")
}
