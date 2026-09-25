package main

import (
	"strings"
	"testing"
)

func TestDefaultTags(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		want   []string
	}{
		{name: "tmux", driver: "interactive-tmux", want: []string{"fast", "hermetic", "tui"}},
		{name: "print", driver: "print-mode", want: []string{"live"}},
		{name: "sdk", driver: "sdk-protocol", want: []string{"fast", "hermetic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultTags(tc.driver)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("defaultTags(%q) = %v, want %v", tc.driver, got, tc.want)
			}
		})
	}
}

func TestRenderScenario(t *testing.T) {
	cases := []struct {
		name string
		cfg  config
		want []string
	}{
		{
			name: "tmux",
			cfg: config{
				Name:   "02-tree-overlay",
				Family: "tree",
				Driver: "interactive-tmux",
				Covers: []string{"packages/coding-agent/src/modes/interactive/interactive-mode.ts"},
				Tags:   []string{"fast", "hermetic", "tui"},
			},
			want: []string{"# Behavior family: tree", "driver = \"interactive-tmux\"", "[tmux]", "both_reach_ready = true", "runtime_ratio_max = 1.20", "PI_SKIP_VERSION_CHECK=1"},
		},
		{
			name: "print",
			cfg: config{
				Name:   "03-bad-model-error",
				Driver: "print-mode",
				Covers: []string{"packages/coding-agent/src/modes/print-mode.ts"},
				Tags:   []string{"fast", "hermetic"},
			},
			want: []string{"driver = \"print-mode\"", "model = \"github-copilot/gpt-5-mini\"", "[print]", "exit_code = 0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderScenario(tc.cfg)
			if err != nil {
				t.Fatalf("renderScenario: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("rendered scenario missing %q\n%s", want, got)
				}
			}
		})
	}
}

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags([]string{
		"-name", "04-model-switch",
		"-family", "model",
		"-driver", "interactive-tmux",
		"-covers", "packages/coding-agent/src/main.ts,packages/coding-agent/src/commands/model.ts",
		"-stdout",
	})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.Name != "04-model-switch" {
		t.Fatalf("Name = %q", cfg.Name)
	}
	if cfg.Family != "model" {
		t.Fatalf("Family = %q", cfg.Family)
	}
	if cfg.Stdout != true {
		t.Fatalf("Stdout = false, want true")
	}
	if got, want := strings.Join(cfg.Tags, ","), "fast,hermetic,tui"; got != want {
		t.Fatalf("Tags = %q, want %q", got, want)
	}
	if len(cfg.Covers) != 2 {
		t.Fatalf("Covers = %v, want 2 entries", cfg.Covers)
	}
}
