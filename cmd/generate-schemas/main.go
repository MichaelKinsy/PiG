// Command generate-schemas writes the JSON Schema artifacts of the configuration files (models.json, settings.json, keybindings.json and
// themes) to schemas/. It is the counterpart of packages/coding-agent/scripts/generate-schemas.ts.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
)

func main() {
	out := flag.String("out", ".", "directory that receives schemas/*.schema.json")
	flag.Parse()
	rendered, err := configschema.Render(codingagent.KeybindingsSchemaArtifact())
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate-schemas:", err)
		os.Exit(1)
	}
	for _, path := range configschema.ArtifactPaths() {
		target := filepath.Join(*out, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "generate-schemas:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(target, []byte(rendered[path]), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "generate-schemas:", err)
			os.Exit(1)
		}
		fmt.Println("Generated", path)
	}
}
