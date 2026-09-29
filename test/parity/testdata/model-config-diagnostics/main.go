package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func main() {
	dir, err := os.MkdirTemp("", "model-config-diagnostics-")
	must(err)
	defer func() { must(os.RemoveAll(dir)) }()
	cwd, err := os.Getwd()
	must(err)
	must(os.Chdir(dir))
	defer func() { must(os.Chdir(cwd)) }()
	for _, tc := range []struct{ name, content string }{
		{"blank", ""},
		{"malformed", "{\n  \"providers\": {\n"},
		{"schema", `{"providers":{"custom":{"models":[{}]}}}`},
		{"directory", ""},
		{"ordinary", `{"providers":{}}`},
		{"missing", ""},
	} {
		path := tc.name + "-models.json"
		switch tc.name {
		case "directory":
			must(os.Mkdir(path, 0o700))
		case "missing":
		default:
			must(os.WriteFile(path, []byte(tc.content), 0o600))
		}
		registry := codingagent.NewModelRegistryWithModelsPath(path)
		fmt.Printf("CASE %s\n%s\nEND\n", tc.name, registry.LoadError())
		if tc.name != "directory" && tc.name != "missing" {
			data, err := os.ReadFile(filepath.Join(dir, path))
			must(err)
			if string(data) != tc.content {
				panic("model loader changed " + path)
			}
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
