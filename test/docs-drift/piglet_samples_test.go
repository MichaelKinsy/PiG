package docsdrift

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
)

// A YAML sample in a Piglet page is a Piglet file or an excerpt of one. PiG's own parser reads it: unknown keys fail, and a complete file must pass the published schema as well. An excerpt has no name, so the parser's complaint about the missing name is the only error it may report.
func TestPigletYAMLSamplesParse(t *testing.T) {
	checked := 0
	for _, s := range allSamples(t) {
		if (s.Lang != "yaml" && s.Lang != "yml") || s.Skip != "" || !pigletPage(s.Src) {
			continue
		}
		if !looksLikePiglet(s.Body) {
			continue
		}
		checked++
		_, err := piglet.ParseBytes([]byte(s.Body))
		if isMissingName(s.Body, err) {
			continue
		}
		if err != nil {
			t.Errorf("%s:%d: PiG's Piglet parser rejects the sample: %v\n%s", s.Src, s.Line, err, s.Body)
			continue
		}
		if strings.HasPrefix(s.Body, "name:") {
			if err := piglet.ValidateAgainstSchema([]byte(s.Body)); err != nil {
				t.Errorf("%s:%d: the Piglet schema rejects the sample: %v", s.Src, s.Line, err)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("recognized only %d Piglet samples; the recognizer is broken", checked)
	}
}

func pigletPage(rel string) bool {
	return strings.Contains(rel, "piglet") || strings.HasSuffix(rel, "publishing.md")
}

// looksLikePiglet recognizes a Piglet file or excerpt by its top-level keys.
func looksLikePiglet(body string) bool {
	for _, key := range []string{"name:", "model:", "tools:", "extensions:", "strip:", "discovery:", "secrets:", "agentEnv:", "extends:", "packages:", "systemPrompt:", "build:", "release:"} {
		if strings.HasPrefix(body, key) || strings.Contains(body, "\n"+key) {
			return true
		}
	}
	return false
}

// isMissingName reports whether err is only the parser's complaint that an excerpt has no name. The parser checks the keys first, so an unknown key reports itself before this error.
func isMissingName(body string, err error) bool {
	return err != nil && !strings.HasPrefix(body, "name:") && !strings.Contains(body, "\nname:") && strings.Contains(err.Error(), `piglet name ""`)
}

func TestPigletSampleCheckRejectsAnUnknownKey(t *testing.T) {
	good := "tools: [read]\n"
	if _, err := piglet.ParseBytes([]byte(good)); !isMissingName(good, err) {
		t.Fatalf("an excerpt with a known key should fail only on the name, got %v", err)
	}
	bad := "tools: [read]\ntoolz: [bash]\n"
	if _, err := piglet.ParseBytes([]byte(bad)); err == nil || isMissingName(bad, err) {
		t.Fatalf("an unknown key must be reported, got %v", err)
	}
}
