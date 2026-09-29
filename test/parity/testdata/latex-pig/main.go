package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/latex"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var input string
		if err := json.Unmarshal(scanner.Bytes(), &input); err != nil {
			return err
		}
		output := struct {
			Input   string  `json:"input"`
			Inline  *string `json:"inline"`
			Display *string `json:"display"`
		}{Input: input}
		if text, ok := latex.RenderLatex(input, latex.RenderLatexOptions{}); ok {
			output.Inline = &text
		}
		if text, ok := latex.RenderLatex(input, latex.RenderLatexOptions{Display: true}); ok {
			output.Display = &text
		}
		if err := enc.Encode(output); err != nil {
			return err
		}
	}
	return scanner.Err()
}
