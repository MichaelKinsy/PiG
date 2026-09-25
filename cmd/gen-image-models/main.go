// Command gen-image-models imports the exact published Pi image catalog and
// emits ai/image_models_generated.go in deterministic provider/model order.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"slices"
	"strings"
)

type imageModel struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	API      string            `json:"api"`
	Provider string            `json:"provider"`
	BaseURL  string            `json:"baseUrl"`
	Headers  map[string]string `json:"headers"`
	Input    []string          `json:"input"`
	Output   []string          `json:"output"`
	Cost     struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
	} `json:"cost"`
}

func main() {
	src := flag.String("src", "", "published image-models.generated.js")
	out := flag.String("out", "ai/image_models_generated.go", "generated Go output")
	flag.Parse()
	if *src == "" {
		fatal(fmt.Errorf("-src is required"))
	}
	models, err := load(*src)
	if err != nil {
		fatal(err)
	}
	data, err := render(models)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "gen-image-models: wrote %d models to %s\n", len(models), *out)
}

func load(path string) ([]imageModel, error) {
	script := `import { pathToFileURL } from "node:url";
const mod = await import(pathToFileURL(process.argv[1]).href);
process.stdout.write(JSON.stringify(mod.IMAGE_MODELS));`
	cmd := exec.Command("node", "--input-type=module", "-e", script, path)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("import published image catalog: %w\n%s", err, data)
	}
	var providers map[string]map[string]imageModel
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&providers); err != nil {
		return nil, fmt.Errorf("decode image catalog: %w", err)
	}
	providerNames := sortedKeys(providers)
	var out []imageModel
	for _, provider := range providerNames {
		ids := sortedKeys(providers[provider])
		for _, id := range ids {
			model := providers[provider][id]
			if model.ID != id || model.Provider != provider {
				return nil, fmt.Errorf("catalog key %s/%s disagrees with model %s/%s", provider, id, model.Provider, model.ID)
			}
			out = append(out, model)
		}
	}
	return out, nil
}

func render(models []imageModel) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated from the exact published upstream image-models.generated.js. DO NOT EDIT.\n")
	b.WriteString("package ai\n\n")
	fmt.Fprintf(&b, "// GeneratedImageModels is the static image model catalog (%d entries).\n", len(models))
	b.WriteString("var GeneratedImageModels = []ImagesModel{\n")
	for _, model := range models {
		fmt.Fprintf(&b, "{ID:%q, Name:%q, API:ImagesAPI(%q), Provider:ImagesProvider(%q), BaseURL:%q,", model.ID, model.Name, model.API, model.Provider, model.BaseURL)
		if len(model.Headers) > 0 {
			b.WriteString(" Headers:map[string]string{")
			for _, key := range sortedKeys(model.Headers) {
				fmt.Fprintf(&b, "%q:%q,", key, model.Headers[key])
			}
			b.WriteString("},")
		}
		fmt.Fprintf(&b, " Input:%#v, Output:%#v, Cost:ImagesCost{Input:%v, Output:%v, CacheRead:%v, CacheWrite:%v}},\n",
			model.Input, model.Output, model.Cost.Input, model.Cost.Output, model.Cost.CacheRead, model.Cost.CacheWrite)
	}
	b.WriteString("}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated catalog: %w", err)
	}
	return formatted, nil
}

func sortedKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen-image-models:", err)
	os.Exit(1)
}
