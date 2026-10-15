package codingagent

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/source-info.ts

// getSyntheticPathSource, isSyntheticPath (source-info.ts:20-26) and createSyntheticSourceInfo (:41-56) against the vendored Pi 1.1.0 module over drawn paths (builtin and angle-bracket
// prefixes, empty and colon-bearing names, lone brackets, unicode) and option sets (each member absent or set, empty strings included).
func TestSourceInfoHelpersMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(149))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	type probe struct {
		Path    string  `json:"path"`
		Source  string  `json:"source"`
		Scope   *string `json:"scope,omitempty"`
		Origin  *string `json:"origin,omitempty"`
		BaseDir *string `json:"baseDir,omitempty"`
		Synth   *string `json:"synth,omitempty"`
	}
	var probes []probe
	for range 4000 {
		p := probe{Path: pick("builtin:", "builtin:read", "<", ">", "<>", "<a>", "<a:b>", "<:b>", "<::>", "<inline:x:y>", "<builtin:read>", "builtin", "a<b>", "<😀:x>", "<a\n>", "  <a>", "<a> ", "/tmp/x.ts", "", "Builtin:read", "<a:>", "<<a>>") + pick("", "", "", ":z", ">", "😀")}
		p.Source = pick("local", "builtin", "", "npm:pkg", "git:x")
		if rng.Intn(2) == 0 {
			p.Scope = new(pick("user", "project", "temporary"))
		}
		if rng.Intn(2) == 0 {
			p.Origin = new(pick("package", "top-level"))
		}
		if rng.Intn(2) == 0 {
			// An empty baseDir is not drawn: Pi keeps `baseDir: ""` and Go's SourceInfo omits an empty one (the empty-string-versus-unset representation ruling in questions/lg-help-4.md).
			p.BaseDir = new(pick("/base", "rel/dir", "/a b/ü"))
		}
		probes = append(probes, p)
	}
	type verdict struct {
		SyntheticSource *string         `json:"syntheticSource"`
		IsSynthetic     bool            `json:"isSynthetic"`
		Info            json.RawMessage `json:"info"`
	}
	var want []verdict
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/source-info.js");
emit(input.map((p) => {
  const options = { source: p.source, ...(p.scope === undefined ? {} : { scope: p.scope }), ...(p.origin === undefined ? {} : { origin: p.origin }), ...(p.baseDir === undefined ? {} : { baseDir: p.baseDir }) };
  return { syntheticSource: mod.getSyntheticPathSource(p.path) ?? null, isSynthetic: mod.isSyntheticPath(p.path), info: mod.createSyntheticSourceInfo(p.path, options) };
}));`, probes, &want)
	if len(want) != len(probes) {
		t.Fatalf("Pi answered %d probes, want %d", len(want), len(probes))
	}
	synthetic := 0
	for i, p := range probes {
		options := SyntheticSourceInfoOptions{Source: p.Source}
		if p.Scope != nil {
			options.Scope = *p.Scope
		}
		if p.Origin != nil {
			options.Origin = *p.Origin
		}
		if p.BaseDir != nil {
			options.BaseDir = *p.BaseDir
		}
		gotSource := SyntheticPathSource(p.Path)
		wantSource := ""
		if want[i].SyntheticSource != nil {
			wantSource = *want[i].SyntheticSource
		}
		if gotSource != wantSource {
			t.Errorf("SyntheticPathSource(%q) = %q, Pi %q", p.Path, gotSource, wantSource)
		}
		if got := IsSyntheticPath(p.Path); got != want[i].IsSynthetic {
			t.Errorf("IsSyntheticPath(%q) = %t, Pi %t", p.Path, got, want[i].IsSynthetic)
		}
		if want[i].IsSynthetic {
			synthetic++
		}
		encoded, err := json.Marshal(CreateSyntheticSourceInfo(p.Path, options))
		if err != nil {
			t.Fatal(err)
		}
		if canonicalSourceInfo(t, encoded) != canonicalSourceInfo(t, want[i].Info) {
			t.Errorf("CreateSyntheticSourceInfo(%q, %+v) = %s, Pi %s", p.Path, options, encoded, want[i].Info)
		}
	}
	if synthetic < len(probes)/4 {
		t.Fatalf("only %d of %d drawn paths are synthetic", synthetic, len(probes))
	}
}

// canonicalSourceInfo re-encodes a SourceInfo JSON through a map so member order does not matter; a member Pi leaves undefined (baseDir) is absent on both sides.
func canonicalSourceInfo(t *testing.T, raw []byte) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
