package packagemanager

import (
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/package-manager.ts

// buildNoMatchingPackageMessage (and so findSuggestedConfiguredSource) for a source the user typed against a list of configured packages: npm packages matched by
// name or by spec, git packages by host/path or host/path@ref, local and unparsable ones never, the first configured package that matches winning, and the query
// trimmed with JS whitespace. Sources whose parse PiG's source model answers differently on purpose (D18) stay out of the corpus.
func TestNoMatchingPackageMessageMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(241))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	configured := []string{
		"npm:pkg", "npm:pkg@1.2.3", "npm:@scope/pkg", "npm:@scope/pkg@^1", "npm: pkg ", "npm:other@latest", "npm:a/b", "npm:pkg@a b",
		"git:github.com/user/repo", "git:github.com/user/repo@v1", "https://github.com/user/repo.git", "https://github.com/user/repo#v2", "git@github.com:user/repo.git",
		"ssh://git@gitlab.com/group/sub/repo", "git:gitlab.com/group/sub/repo@main", "https://example.com/a/b.git@tag", "git:user/repo", "github.com/user/other",
		"./local", "/abs/path", "~/x", "../up", "file:///tmp/x",
	}
	queries := []string{
		"pkg", "pkg@1.2.3", "@scope/pkg", "@scope/pkg@^1", " pkg ", "\u00a0pkg\ufeff", "\u0085pkg", "other", "other@latest", "a/b", "pkg@a b", "nothing", "", " ", "npm:pkg",
		"github.com/user/repo", "github.com/user/repo@v1", "github.com/user/repo@v2", "github.com/user/repo@", "gitlab.com/group/sub/repo", "gitlab.com/group/sub/repo@main",
		"example.com/a/b", "example.com/a/b@tag", "github.com/user/other", " github.com/user/repo ", "user/repo", "https://github.com/user/repo", "./local", "local",
	}
	type probe struct {
		Source     string   `json:"source"`
		Configured []string `json:"configured"`
	}
	var probes []probe
	for range 2500 {
		p := probe{Source: queries[rng.Intn(len(queries))]}
		for range 1 + rng.Intn(4) {
			p.Configured = append(p.Configured, configured[rng.Intn(len(configured))])
		}
		if rng.Intn(6) == 0 {
			p.Source = pick("pkg", "github.com/user/repo")
		}
		probes = append(probes, p)
	}
	var want []string
	pioracle.Run(t, `
const { DefaultPackageManager } = await load("pi-coding-agent/core/package-manager.js");
const manager = new DefaultPackageManager({ cwd: "/work/cwd", agentDir: "/agent", settingsManager: {} });
emit(input.map(({ source, configured }) => manager.buildNoMatchingPackageMessage(source, configured.map((s, i) => (i % 2 ? { source: s } : s)))));`, probes, &want)
	suggested, failures := 0, 0
	for i, p := range probes {
		var pkgs []ConfiguredPackage
		for _, source := range p.Configured {
			pkgs = append(pkgs, ConfiguredPackage{Source: codingagent.PackageSource{Source: source}})
		}
		got := noMatchingPackageMessage(p.Source, pkgs)
		if len(got) > len("No matching package found for "+p.Source) {
			suggested++
		}
		if got != want[i] {
			if failures++; failures <= 10 {
				t.Errorf("%q over %q: PiG %q, Pi %q", p.Source, p.Configured, got, want[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
	if suggested < 200 || suggested > len(probes)-200 {
		t.Fatalf("%d of %d probes produced a suggestion", suggested, len(probes))
	}
}
