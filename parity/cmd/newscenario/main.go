package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type config struct {
	Name   string
	Family string
	Driver string
	Covers []string
	Tags   []string
	Model  string
	Output string
	Force  bool
	Stdout bool
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("newscenario", flag.ContinueOnError)
	fs.SetOutput(new(bytes.Buffer))

	var cfg config
	var covers string
	var tags string
	fs.StringVar(&cfg.Name, "name", "", "scenario name, usually NN-short-description")
	fs.StringVar(&cfg.Family, "family", "", "behavior family directory under parity/scenarios/ (e.g. model, settings, tree)")
	fs.StringVar(&cfg.Driver, "driver", "interactive-tmux", "driver: interactive-tmux | print-mode | sdk-protocol")
	fs.StringVar(&covers, "covers", "", "comma-separated upstream paths from PORT_MAP (.upstream/current/packages/...) ")
	fs.StringVar(&tags, "tags", "", "comma-separated tags; defaults by driver")
	fs.StringVar(&cfg.Model, "model", "", "model for print-mode/live scenarios")
	fs.StringVar(&cfg.Output, "output", "", "output file path (default parity/scenarios[/<family>]/<name>.toml)")
	fs.BoolVar(&cfg.Force, "force", false, "overwrite existing file")
	fs.BoolVar(&cfg.Stdout, "stdout", false, "write to stdout instead of a file")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.Name == "" {
		return config{}, errors.New("-name is required")
	}
	cfg.Covers = splitCSV(covers)
	if len(cfg.Covers) == 0 {
		return config{}, errors.New("-covers must list at least one upstream path")
	}
	cfg.Tags = splitCSV(tags)
	if len(cfg.Tags) == 0 {
		cfg.Tags = defaultTags(cfg.Driver)
	}
	if cfg.Output == "" && !cfg.Stdout {
		if cfg.Family != "" {
			cfg.Output = filepath.Join("parity", "scenarios", cfg.Family, cfg.Name+".toml")
		} else {
			cfg.Output = filepath.Join("parity", "scenarios", cfg.Name+".toml")
		}
	}
	if err := validateConfig(cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func run(cfg config) error {
	body, err := renderScenario(cfg)
	if err != nil {
		return err
	}
	if cfg.Stdout {
		_, err := fmt.Print(body)
		return err
	}
	if !cfg.Force {
		if _, err := os.Stat(cfg.Output); err == nil {
			return fmt.Errorf("output exists: %s (use -force to overwrite)", cfg.Output)
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Output), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.Output, []byte(body), 0o644)
}

func validateConfig(cfg config) error {
	switch cfg.Driver {
	case "interactive-tmux", "print-mode", "sdk-protocol":
	default:
		return fmt.Errorf("unsupported -driver %q", cfg.Driver)
	}
	return nil
}

func renderScenario(cfg config) (string, error) {
	var b strings.Builder
	if cfg.Family != "" {
		fmt.Fprintf(&b, "# Behavior family: %s\n", cfg.Family)
		b.WriteString("# PORT_MAP linkage lives in covers = [...]. Keep it honest and explicit.\n\n")
	}
	fmt.Fprintf(&b, "name = %q\n", cfg.Name)
	fmt.Fprintf(&b, "description = %q\n", "TODO: describe the observable parity contract")
	writeStringList(&b, "covers", cfg.Covers)
	writeStringList(&b, "tags", cfg.Tags)
	fmt.Fprintf(&b, "driver = %q\n", cfg.Driver)
	if cfg.Model != "" {
		fmt.Fprintf(&b, "model = %q\n", cfg.Model)
	}
	b.WriteString("\n")

	switch cfg.Driver {
	case "interactive-tmux":
		b.WriteString(`[tmux]
width = 100
height = 35
ready_pattern_pig = "Ready. Type a message"
ready_pattern_pi = "pi v"
ready_timeout_seconds = 15
keys = [
  # "hello",
  # "Enter",
]
settle_seconds = 1

[env]
# pi: PI_SKIP_VERSION_CHECK=1 is always included to suppress upstream's
# update-available banner from clobbering the cropped output region.
pi = ["PI_SKIP_VERSION_CHECK=1"]
# Uncomment and adjust if the scenario needs a custom agent/home dir.
# The snapshot mechanism copies these to t.TempDir(): paths must resolve
# from this file's directory. See AGENTS.md → Scenario isolation rules.
# pig = ["PIG_CODING_AGENT_DIR=testdata/pig-agent"]
# pi   = ["PI_SKIP_VERSION_CHECK=1", "PI_CODING_AGENT_DIR=testdata/pi-agent"]
#
# Session-dir uses {{TEMP}}: the runner expands this to a fresh
# per-binary, per-run tempdir. Never hard-code /tmp paths; the lint
# rejects them. See parity/runner/tokens.go for full rationale.
pig_args = ["--no-extensions", "--session-dir", "{{TEMP}}/sessions"]
pi_args = ["--session-dir", "{{TEMP}}/sessions"]

# Scheduler note: interactive-tmux defaults to group = "tmux".
# Only add an explicit group override when the default bucket is wrong for
# this surface. If you do, include a '# group-rationale:' comment above the
# scenario header. The linter enforces this so schedule exceptions remain
# intentional and auditable.
`)

		b.WriteString(`
[assert]
both_reach_ready = true
# both_contain = ["TODO"]
# both_not_contain = ["panic:", "goroutine "]
runtime_ratio_max = 1.20
runs = 1
`)
	case "print-mode":
		if cfg.Model == "" {
			b.WriteString("model = \"github-copilot/gpt-5-mini\"\n\n")
		}
		b.WriteString(`[print]
prompt = "TODO: prompt"
timeout_seconds = 60

# Scheduler note: print-mode defaults to group = "process".
# Override only for a real concurrency reason and document it with
# '# group-rationale:' above the scenario header.

[assert]
exit_code = 0
# both_contain = ["TODO"]
# both_not_contain = ["panic:", "goroutine "]
runtime_ratio_max = 1.20
runs = 1
`)
	case "sdk-protocol":
		b.WriteString(`# TODO: sdk-protocol driver not implemented yet.
# Fill this scenario in once the driver lands.

[assert]
# output_normalized_equal = true
runs = 1
`)
	}
	return b.String(), nil
}

func writeStringList(b *strings.Builder, key string, vals []string) {
	fmt.Fprintf(b, "%s = [\n", key)
	for _, v := range vals {
		fmt.Fprintf(b, "  %q,\n", v)
	}
	b.WriteString("]\n")
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultTags(driver string) []string {
	switch driver {
	case "interactive-tmux":
		return []string{"fast", "hermetic", "tui"}
	case "print-mode":
		return []string{"live"}
	case "sdk-protocol":
		return []string{"fast", "hermetic"}
	default:
		return nil
	}
}

// (sanitizeSessionSuffix was used to build unique /tmp paths in
// scenarios. The {{TEMP}} substitution token has replaced literal
// session-dir paths, so the helper is no longer needed. Kept removed
// rather than left dead to satisfy go vet / deadcode.)
