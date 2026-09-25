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
		"coding/upstream.go":              "package coding\n\nconst UpstreamReviewedVersion = \"9.9.0\"\n",
		"coding/pigversion/pigversion.go": "package pigversion\n\nconst UpstreamVersion = \"9.9.9\"\n",
		"coding/piglet/main.go": "package piglet\n\nimport \"io\"\n\nfunc printHelp(w io.Writer) {\n" +
			"\tprint(w, `pig piglet list\npig piglet build\n`)\n}\n",
		"AGENTS.md": "<!-- BEGIN COVERAGE -->\n**Porting:** 40 / 50 intended-portable entries ✅ (80.0%); **Verification:** 30 behavioral (75.0%), 10 untested.\n<!-- END COVERAGE -->\n",
		"README.md": "<!-- BEGIN PORTING -->\n**Porting:** 40 of 50 intended-portable upstream files are ported (80.0%).\n\n**Parity coverage badge:** 30 of the 40 ported files (75.0%) have at least one behavioral parity scenario.\n<!-- END PORTING -->\n",
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
		"cmd/pig/help.go": "package main\n\n// This comment may say full parity.\nconst help = \"pig targets Pi 9.9.9\"\n",
	})
	if output, err := runPublicClaims(t, root); err != nil {
		t.Fatalf("supported statements were rejected:\n%s", output)
	}
}

func TestPublicClaimsCheckRejectsContradictedStatements(t *testing.T) {
	root := writeClaimsFixture(t, map[string]string{
		"README.md": "<!-- BEGIN PORTING -->\n**Porting:** 45 of 50 intended-portable upstream files are ported (90.0%).\n<!-- END PORTING -->\n",
		"docs/site/docs/index.md": "PiG has full parity with Pi and sends the same requests byte for byte.\n" +
			"PiG implements every Pi command.\n" +
			"It tracks Pi 9.8.0 and Pi 9.9.0.\n" +
			"Porting is 352/359 ported (98.1%).\n" +
			"PiG is sponsored by HPE's Open Source Program Office.\n",
		"cmd/pig/help.go":          "package main\n\nconst help = `pig\nmirrors every Pi command`\n\nvar desc = \"a drop-in replacement for Pi 9.7.0\"\n",
		"docs/site/app/strings.ts": "export const tagline = 'PiG is feature-complete';\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("contradicted statements passed:\n%s", output)
	}
	for _, want := range []string{
		"readme: porting block disagrees with the AGENTS.md coverage block",
		"forbidden phrase 'full parity'",
		"forbidden phrase 'same requests'",
		"forbidden phrase 'byte for byte'",
		"forbidden phrase 'every Pi command'",
		"Pi 9.8.0 is not the pinned Pi 9.9.9",
		"Pi 9.9.0 is not the pinned Pi 9.9.9",
		"98.1% is not a current porting (80.0%) or verification (75.0%) figure",
		"352/359 is not the current 40/50 porting",
		"unrecorded claim 'sponsored by'",
		"cmd/pig/help.go:4: phrase: forbidden phrase 'every Pi command'",
		"cmd/pig/help.go:6: phrase: forbidden phrase 'drop-in replacement'",
		"cmd/pig/help.go:6: version: Pi 9.7.0 is not the pinned Pi 9.9.9",
		"docs/site/app/strings.ts:1: phrase: forbidden phrase 'feature-complete'",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not report %q:\n%s", want, output)
		}
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
		"cmd/pig/help.go": "package main\n/* \"full parity\"\n`every Pi command` */\n// \"same requests\"\nvar quote = '\"'\nvar slash = '/'\nconst text = \"https://example.test/\\\" full parity\"\nconst raw = `https://example.test/\nevery Pi command`\n",
	})
	output, err := runPublicClaims(t, root)
	if err == nil {
		t.Fatalf("claims hidden by lexical boundaries passed: %s", output)
	}
	for _, want := range []string{
		"cmd/pig/help.go:7: phrase: forbidden phrase 'full parity'",
		"cmd/pig/help.go:9: phrase: forbidden phrase 'every Pi command'",
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
