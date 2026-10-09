package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// packages/coding-agent/test/changelog.test.ts, ported with the same inputs and expected results.
func TestNormalizeChangelogLinksPortedUpstreamCases(t *testing.T) {
	entry := ChangelogEntry{Major: 0, Minor: 79, Patch: 0}
	got := NormalizeChangelogLinks(strings.Join([]string{
		"[Project Trust](README.md#project-trust)",
		"[Extensions](docs/extensions.md#project_trust)",
		"[Examples](examples/extensions/)",
		"[Root README](../../README.md#supply-chain-hardening)",
	}, "\n"), entry.Version())
	want := strings.Join([]string{
		"[Project Trust](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/README.md#project-trust)",
		"[Extensions](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/docs/extensions.md#project_trust)",
		"[Examples](https://github.com/earendil-works/pi/tree/v0.79.0/packages/coding-agent/examples/extensions/)",
		"[Root README](https://github.com/earendil-works/pi/blob/v0.79.0/README.md#supply-chain-hardening)",
	}, "\n")
	if got != want {
		t.Errorf("package-relative links:\n got %q\nwant %q", got, want)
	}
	got = NormalizeChangelogLinks(strings.Join([]string{
		"[#5167](https://github.com/earendil-works/pi-mono/pull/5167)",
		"[#4163](https://github.com/badlogic/pi-mono/issues/4163)",
		"[Agent README](https://github.com/badlogic/pi-mono/blob/main/packages/agent/README.md)",
		"[External](https://example.com/docs)",
		"[Local anchor](#settings)",
	}, "\n"), "0.79.0")
	want = strings.Join([]string{
		"[#5167](https://github.com/earendil-works/pi/pull/5167)",
		"[#4163](https://github.com/earendil-works/pi/issues/4163)",
		"[Agent README](https://github.com/earendil-works/pi/blob/v0.79.0/packages/agent/README.md)",
		"[External](https://example.com/docs)",
		"[Local anchor](#settings)",
	}, "\n")
	if got != want {
		t.Errorf("canonicalized repository URLs:\n got %q\nwant %q", got, want)
	}
}

// utils/changelog.ts normalizeChangelogLinks against pinned Pi over link shapes: relative, absolute, parent, query and fragment,
// directory and file targets, spaces and non-ASCII (encodeURI), titles, images, legacy and floating-ref repository URLs, schemes,
// protocol-relative and anchor targets, and text that only looks like a link.
func TestNormalizeChangelogLinksMatchesPi(t *testing.T) {
	targets := []string{
		"README.md", "./README.md", "docs/a b.md", "docs/ünï.md#frag", "docs/x.md?plain=1#L10", "/docs/x.md", "//cdn.example.com/x", "#top", "mailto:a@b.c",
		"ftp://example.com/x", "HTTP://EXAMPLE.COM", "../../README.md", "../../../outside.md", "../..", "..", ".", "./", "examples/", "examples", "src/dir.v2/",
		"src/dir.v2", "docs\\win\\path.md", "a/../b.md", "a//b.md", "docs/x.md#a#b", "docs/[x].md", "docs/100%.md", "docs/a'b(c)d.md", "docs/日本語.md", "docs/a;b,c.md",
		"https://github.com/badlogic/pi-mono", "https://github.com/badlogic/pi-mono/", "https://github.com/badlogic/pi-mono-extra", "https://github.com/earendil-works/pi-mono/pull/1",
		"https://github.com/earendil-works/pi/blob/main/README.md", "https://github.com/earendil-works/pi/tree/master/docs", "https://github.com/earendil-works/pi/blob/main",
		"https://github.com/badlogic/pi-mono/blob/master/x.md#l1", "https://github.com/other/pi-mono/blob/main/x.md", "https://example.com/blob/main/x",
	}
	var probes []map[string]string
	for _, target := range targets {
		for _, shape := range []string{"[t](%s)", "![img](%s)", "[t](%s \"title here\")", "[t](%s   'x')", "text [a](%s) and [b](%s) end"} {
			probes = append(probes, map[string]string{"markdown": strings.ReplaceAll(shape, "%s", target), "version": "1.2.3"})
		}
	}
	probes = append(probes,
		map[string]string{"markdown": "[t](README.md)", "version": "v0.5.0"},
		map[string]string{"markdown": "[t](README.md)", "version": "vv1"},
		map[string]string{"markdown": "[a\nb](README.md) [](x.md) [ ](y.md) [t]( spaced.md) [t](a.md\n)", "version": "1.0.0"},
		map[string]string{"markdown": "No links here, ](x) [y] (z)", "version": "1.0.0"},
		map[string]string{"markdown": "[a](b.md)[c](d.md)\n[e](f.md \"t\")", "version": "1.0.0"},
		map[string]string{"markdown": "[t](\u00a0x.md) [t](x.md\u3000\"z\")", "version": "1.0.0"},
	)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/changelog_links.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, probe := range probes {
		if got := NormalizeChangelogLinks(probe["markdown"], probe["version"]); got != expected[i] {
			if failures++; failures <= 8 {
				t.Errorf("%q @%s:\n  Pig %q\n  Pi  %q", probe["markdown"], probe["version"], got, expected[i])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
