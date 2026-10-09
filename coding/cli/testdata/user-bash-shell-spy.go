// Executable shell spy for the RPC user_bash port. It records the command and returns the upstream test's local result.
package main

import (
	"fmt"
	"os"
)

func main() {
	file, err := os.OpenFile(os.Getenv("PORT_BASH_COUNTER"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		panic(err)
	}
	if _, err := fmt.Fprintln(file, os.Args[len(os.Args)-1]); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	fmt.Print("local output")
}
