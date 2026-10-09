package docsdrift

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const publicClaimsScript = "../../automation/ci/check-public-claims.py"

func runPublicClaims(t *testing.T, root string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(publicClaimsScript)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", script, "--root", root)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// The repository's public prose must agree with the pin, the generated
// coverage block, and the recorded governance state.
func TestPublicClaimsMatchTheEvidence(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := runPublicClaims(t, root); err != nil {
		t.Fatalf("public claims contradict the evidence:\n%s", output)
	}
}

func writeClaimsFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	base := map[string]string{
		"coding/upstream.go":                       "package coding\n\nconst UpstreamReviewedVersion = \"9.9.0\"\n",
		"internal/coding/pigversion/pigversion.go": "package pigversion\n\nconst UpstreamVersion = \"9.9.9\"\n",
		"coding/piglet/main.go": "package piglet\n\nimport \"io\"\n\nfunc printHelp(w io.Writer) {\n" +
			"\tprint(w, `pig piglet list\npig piglet build\n`)\n}\n",
		"AGENTS.md": "<!-- BEGIN COVERAGE -->\n**Porting:** 40 / 50 intended-portable entries ✅ (80.0%); **Verification:** 30 behavioral (75.0%), 10 untested.\n<!-- END COVERAGE -->\n",
		"README.md": "# PiG\n",
	}
	maps.Copy(base, files)
	for name, body := range base {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPublicClaimsCheckAcceptsSupportedStatements(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"docs/site/docs/index.md": "PiG pins Pi 9.9.9. Porting reads 40/50 (80.0%).\n" +
			"Tool definitions and their order match Pi byte for byte.\n" +
			"This page does not claim HPE sponsorship or that PiG is sponsored by an Open Source Program Office.\n" +
			"The ledgers were last reviewed against Pi 9.9.0.\n",
		// Comments are not user-visible; only string literals are checked.
		"coding/cli/help.go": "package main\n\n// This comment may say full parity.\nconst help = \"pig targets Pi 9.9.9\"\n",
	})
	if output, err := runPublicClaims(t, root); err != nil {
		t.Fatalf("supported statements were rejected:\n%s", output)
	}
}

func TestPublicClaimsCheckRejectsContradictedStatements(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"docs/site/docs/index.md": "PiG has full parity with Pi and sends the same requests byte for byte.\n" +
			"PiG implements every Pi command.\n" +
			"It tracks Pi 9.8.0 and Pi 9.9.0.\n" +
			"Porting is 352/359 ported (98.1%).\n" +
			"PiG is sponsored by HPE's Open Source Program Office.\n",
		"coding/cli/help.go":       "package main\n\nconst help = `pig\nmirrors every Pi command`\n\nvar desc = \"a drop-in replacement for Pi 9.7.0\"\n",
		"docs/site/app/strings.ts": "export const tagline = 'PiG is feature-complete';\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("contradicted statements passed:\n%s", output)
	}
	for _, want := range []string{
		"forbidden phrase 'full parity'",
		"forbidden phrase 'same requests'",
		"forbidden phrase 'byte for byte'",
		"forbidden phrase 'every Pi command'",
		"Pi 9.8.0 is not the pinned Pi 9.9.9",
		"Pi 9.9.0 is not the pinned Pi 9.9.9",
		"98.1% is not a current porting (80.0%) or verification (75.0%) figure",
		"352/359 is not the current 40/50 porting",
		"unrecorded claim 'sponsored by'",
		"coding/cli/help.go:4: phrase: forbidden phrase 'every Pi command'",
		"coding/cli/help.go:6: phrase: forbidden phrase 'drop-in replacement'",
		"coding/cli/help.go:6: version: Pi 9.7.0 is not the pinned Pi 9.9.9",
		"docs/site/app/strings.ts:1: phrase: forbidden phrase 'feature-complete'",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not report %q:\n%s", want, output)
		}
	}
}

// The generated block states the per-scope and per-package figures as well as the headline;
// prose may quote any of them and nothing else. Each figure below appears in exactly one
// place of the block, so dropping the scope lines, the package rows or the behavioral
// side of either fails, and figures after the END marker are not part of the block.
func TestPublicClaimsCheckAcceptsEveryFigureOfTheGeneratedBlock(t *testing.T) {
	agents := "<!-- BEGIN COVERAGE -->\n**Porting:** 40 / 50 intended-portable entries ✅ (80.0%); **Verification:** 30 behavioral (75.0%), 10 untested.\n\n" +
		"| package | src files | n/a | intended | ✅ | 🟡 | ⬜ | ported | behavioral |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n" +
		"| `core` | 20 | 2 | 18 | 15 | 3 | 0 | 83.3% | 60.0% |\n" +
		"| `edge` | 15 | 3 | 12 | 7 | 5 | 0 | 58.3% | 42.9% |\n\n" +
		"- **Core packages (core, edge):** 22 / 30 intended-portable files ✅ (73.3%) from 35 rows; 12 behavioral (54.5%), 1 weak-only, 9 untested.\n" +
		"<!-- END COVERAGE -->\n\n" +
		"| `stale` | 12 | 0 | 12 | 11 | 1 | 0 | 91.7% | 91.7% |\n" +
		"- **Stale scope:** 11 / 12 intended-portable files ✅ (91.7%) from 12 rows; 10 behavioral (90.9%), 0 weak-only, 1 untested.\n"
	root := writeClaimsFixture(t, map[string]string{
		"AGENTS.md": agents,
		"docs/site/docs/index.md": "Core packages: 22/30 ported (73.3%), 12/22 ported verified (54.5%).\n" +
			"The core package is 83.3% ported and 60.0% verified; edge is 58.3% ported and 42.9% verified.\n" +
			"The whole port is 40/50 (80.0%).\n",
	})
	if output, err := runPublicClaims(t, root); err != nil {
		t.Fatalf("figures of the generated block were rejected:\n%s", output)
	}
	root = writeClaimsFixture(t, map[string]string{
		"AGENTS.md":               agents,
		"docs/site/docs/index.md": "Core packages: 16/18 ported (88.8%).\nThe stale scope: 11/12 ported (91.7%), 90.9% verified.\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("a figure the block does not state passed:\n%s", output)
	}
	for _, want := range []string{
		"88.8% is not a current porting", "16/18 is not the current 40/50 porting",
		"91.7% is not a current porting", "90.9% is not a current porting", "11/12 is not the current 40/50 porting",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not report %q:\n%s", want, output)
		}
	}
}

const versionRecords = `[[record]]
version = "9.8.0"
reason = "A probe report."
paths = ["docs/findings/probe.md", "docs/plan/"]
`

// Released changelog sections and reviewed records keep the release they
// describe. The pinned release stays required everywhere else.
func TestPublicClaimsCheckAcceptsRecordsOfAnEarlierRelease(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"automation/ci/version-records.toml": versionRecords,
		"docs/findings/probe.md":             "The probe ran against Pi 9.8.0.\n",
		"docs/plan/progress/lane.md":         "The lane starts from Pi 9.8.0.\n",
		"CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n- Match Pi 9.9.9.\n\n" +
			"## [1.0.0] - 2026-01-01\n\n- Ported Pi 9.8.0.\n\n## [0.0.0] - Development baseline\n\n- Started at Pi 9.7.0.\n",
	})
	if output, err := runPublicClaims(t, root); err != nil {
		t.Fatalf("records of an earlier release were rejected:\n%s", output)
	}
}

func TestPublicClaimsCheckRejectsUnreviewedOrStaleRecords(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"automation/ci/version-records.toml": versionRecords + `
[[record]]
version = "9.8.0"
reason = "A report that no longer names the release."
paths = ["docs/findings/current.md", "docs/findings/gone.md"]
`,
		"docs/findings/probe.md":   "The probe ran against Pi 9.8.0 and Pi 9.7.0.\n",
		"docs/findings/current.md": "The probe ran against Pi 9.9.9.\n",
		"docs/findings/new.md":     "The probe ran against Pi 9.8.0.\n",
		"CHANGELOG.md":             "# Changelog\n\n## [Unreleased]\n\n- Match Pi 9.8.0.\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("unreviewed or stale records passed:\n%s", output)
	}
	for _, want := range []string{
		"docs/findings/probe.md:1: version: Pi 9.7.0 is not the pinned Pi 9.9.9",
		"docs/findings/new.md:1: version: Pi 9.8.0 is not the pinned Pi 9.9.9",
		"CHANGELOG.md:5: version: Pi 9.8.0 is not the pinned Pi 9.9.9",
		"automation/ci/version-records.toml: docs/findings/current.md (9.8.0) matches nothing; remove the entry",
		"automation/ci/version-records.toml: docs/findings/gone.md (9.8.0) names a missing file; remove the entry",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not report %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "docs/findings/probe.md:1: version: Pi 9.8.0") {
		t.Errorf("the reviewed release in a listed record produced a finding:\n%s", output)
	}
}

func TestPublicClaimsCheckRejectsUndocumentedPigletCommandsOutsidePlannedSections(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"docs/piglets.md": "# Piglets\n\npig piglet list\n\n## Planned (not in this release)\n\npig piglet pull demo\n\n### Details\n\npig piglet publish demo\n\n## Current behavior\n\npig piglet update demo\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("an unlisted Piglet command passed outside a Planned section:\n%s", output)
	}
	for _, want := range []string{
		"docs/piglets.md:15: piglet: pig piglet update is absent from pig piglet --help",
		"move future syntax under a Planned heading",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not report %q:\n%s", want, output)
		}
	}
	for _, planned := range []string{"pig piglet pull", "pig piglet publish"} {
		if strings.Contains(output, planned+" is absent") {
			t.Errorf("planned command %q produced a finding:\n%s", planned, output)
		}
	}
}

func TestPublicClaimsGoLexicalBoundaries(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"coding/cli/help.go": "package main\n/* \"full parity\"\n`every Pi command` */\n// \"same requests\"\nvar quote = '\"'\nvar slash = '/'\nconst text = \"https://example.test/\\\" full parity\"\nconst raw = `https://example.test/\nevery Pi command`\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("claims hidden by lexical boundaries passed: %s", output)
	}
	for _, want := range []string{
		"coding/cli/help.go:7: phrase: forbidden phrase 'full parity'",
		"coding/cli/help.go:9: phrase: forbidden phrase 'every Pi command'",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in %s", want, output)
		}
	}
	for _, unwanted := range []string{"help.go:2:", "help.go:3:", "help.go:4:"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("comment produced a finding: %s", output)
		}
	}
}
