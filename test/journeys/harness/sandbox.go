package harness

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Kind names the product under test.
type Kind string

// The two products every journey runs on.
const (
	PiG Kind = "pig"
	Pi  Kind = "pi"
)

// SandboxOpts customises one product's sandbox.
type SandboxOpts struct {
	Kind     Kind
	Root     string // private directory for everything this run writes
	Provider *Provider
	// Settings is merged into settings.json.
	Settings map[string]any
	// Files are created under the working directory before launch (path -> content).
	Files map[string]string
	// AgentFiles are created under the agent directory before launch (path -> content): user prompts, skills and context files.
	AgentFiles map[string]string
	// Bin holds executable scripts (name -> content) that shadow host commands: the sandbox bin directory leads PATH.
	Bin map[string]string
	// ExtraEnv is appended to the child environment.
	ExtraEnv []string
	// Reasoning marks the scripted model as a reasoning model.
	Reasoning bool
	// Images adds image input support to the scripted model.
	Images bool
	// ContextWindow overrides the scripted model's context window.
	ContextWindow int
}

// Sandbox holds the directories one product instance runs in.
type Sandbox struct {
	Kind     Kind
	Root     string
	Home     string
	Agent    string
	Cwd      string
	Sessions string
	Tmp      string
	Env      []string
}

// ModelRef is the --model value for the scripted model.
const ModelRef = "strict-wire/strict"

// NewSandbox lays out directories, models.json and settings.json, and builds the child environment.
func NewSandbox(o SandboxOpts) (*Sandbox, error) {
	s := &Sandbox{Kind: o.Kind, Root: o.Root}
	s.Home = filepath.Join(o.Root, "home")
	s.Agent = filepath.Join(o.Root, "agent")
	s.Cwd = filepath.Join(o.Root, "work")
	s.Sessions = filepath.Join(o.Root, "sessions")
	s.Tmp = filepath.Join(o.Root, "tmp")
	for _, d := range []string{s.Home, s.Agent, s.Cwd, s.Sessions, s.Tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	input := []string{"text"}
	if o.Images {
		input = append(input, "image")
	}
	window := o.ContextWindow
	if window == 0 {
		window = 128000
	}
	models := map[string]any{"providers": map[string]any{"strict-wire": map[string]any{
		"api": "openai-completions", "baseUrl": o.Provider.URL(), "apiKey": "fixture-key",
		"models": []any{map[string]any{"id": "strict", "name": "strict", "reasoning": o.Reasoning, "input": input,
			"cost": json.RawMessage(`{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`), "contextWindow": window, "maxTokens": 4096}},
	}}}
	if err := writeJSON(filepath.Join(s.Agent, "models.json"), models); err != nil {
		return nil, err
	}
	settings := map[string]any{}
	maps.Copy(settings, o.Settings)
	if err := writeJSON(filepath.Join(s.Agent, "settings.json"), settings); err != nil {
		return nil, err
	}
	for name, content := range o.Files {
		p := filepath.Join(s.Cwd, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	binDir := filepath.Join(s.Root, "bin")
	for name, content := range o.Bin {
		if err := os.MkdirAll(binDir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o700); err != nil {
			return nil, err
		}
	}
	for name, content := range o.AgentFiles {
		p := filepath.Join(s.Agent, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + s.Home,
		"TMPDIR=" + s.Tmp,
		"USER=journey",
		"LANG=C.UTF-8",
		"TERM=tmux-256color",
		"COLORTERM=truecolor",
		"SHELL=/bin/sh",
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
	}
	// Keep the shared Go, Node and Cargo caches: a sandboxed HOME must not force a rebuild or reinstall.
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOFLAGS", "GOPATH", "GOMAXPROCS", "GOMEMLIMIT", "CARGO_HOME", "RUSTUP_HOME", "CARGO_BUILD_JOBS", "XDG_CACHE_HOME", "NPM_CONFIG_CACHE", "MISE_DATA_DIR", "MISE_CONFIG_DIR", "GOTOOLCHAIN", "LD_LIBRARY_PATH"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	switch o.Kind {
	case PiG:
		env = append(env, "PIG_HOME="+filepath.Join(s.Home, ".pig"), "PIG_CODING_AGENT_DIR="+s.Agent, "PIG_QUIET_STARTUP=1", "PIG_USE_PI_DIRS=")
	case Pi:
		env = append(env, "PI_CODING_AGENT_DIR="+s.Agent)
	}
	s.Env = slices.Concat(env, o.ExtraEnv)
	return s, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// SessionFiles lists the session JSONL files under the sandbox, oldest name first.
func (s *Sandbox) SessionFiles() []string {
	var out []string
	_ = filepath.WalkDir(s.Sessions, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// String identifies the sandbox in failure messages.
func (s *Sandbox) String() string { return fmt.Sprintf("%s@%s", s.Kind, s.Root) }
