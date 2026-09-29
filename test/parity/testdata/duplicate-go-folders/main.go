package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	codingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.MkdirTemp("", "duplicate-folders-go-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	var configs []subprocess.ExtConfig
	for i := range 2 {
		dir := filepath.Join(root, fmt.Sprintf("copy-%d", i), "ask")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		files := map[string]string{
			"go.mod": fmt.Sprintf("module example.com/ask\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.ToSlash(filepath.Join(cwd, "extensions", "sdk"))),
			"ext.go": fmt.Sprintf(`package ask
import ("fmt"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension {e:=sdk.New("ask"); calls:=0; e.Command("ask","copy-%d",func(sdk.Context,string)error{calls++;return fmt.Errorf("copy-%d:%%d",calls)}); e.Tool("shared","shared",sdk.Schema{"type":"object"},func(sdk.Context,map[string]any)(any,error){return map[string]any{"content":[]any{}},nil});return e}
`, i, i),
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				return err
			}
		}
		cfg, _, err := subprocess.ResolveExtConfig(dir)
		if err != nil {
			return err
		}
		configs = append(configs, cfg)
	}
	h := subprocess.NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	defer h.Shutdown("probe done")
	h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	for _, phase := range []string{"load", "reload"} {
		var loaded []extension.Extension
		if phase == "load" {
			var errs []error
			loaded, errs = h.LoadAll(context.Background(), configs)
			if len(errs) != 0 {
				return fmt.Errorf("load: %v", errs)
			}
		} else {
			loaded, err = h.Reload(context.Background())
			if err != nil {
				return err
			}
			if issues := h.LastReloadReport().Issues; len(issues) != 0 {
				return fmt.Errorf("reload: %v", issues)
			}
		}
		calls := []string{}
		for _, ext := range loaded {
			err := ext.Commands["ask"].Handler(context.Background(), "")
			if err == nil {
				return fmt.Errorf("command did not report its copy")
			}
			calls = append(calls, err.Error())
		}
		type conflict struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		}
		conflicts := []conflict{}
		for _, c := range codingagent.DetectExtensionConflicts(loaded) {
			path, err := filepath.Rel(root, c.Path)
			if err != nil {
				return err
			}
			conflicts = append(conflicts, conflict{filepath.ToSlash(path), strings.ReplaceAll(c.Message, root+string(filepath.Separator), "")})
		}
		record := struct {
			Phase     string     `json:"phase"`
			Calls     []string   `json:"calls"`
			Conflicts []conflict `json:"conflicts"`
		}{phase, calls, conflicts}
		if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
			return err
		}
	}
	return nil
}
