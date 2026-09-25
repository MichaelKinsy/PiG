package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/parity/porter"
)

func main() {
	if err := run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pig-porter:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, output io.Writer) error {
	request, err := porter.DecodeRequest(input)
	if err != nil {
		return err
	}
	response, err := porter.Execute(ctx, request)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(response)
}
