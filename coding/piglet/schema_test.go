package piglet

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAC1SchemaParserAgreement pins R1/AC-1: the published closed Piglet JSON
// Schema v1 and Go parsing accept or reject the same closed vocabulary. The
// expected column is the independent oracle; the schema and parser must agree
// with each other and with it.
func TestAC1SchemaParserAgreement(t *testing.T) {
	cases := []struct {
		name  string
		yaml  string
		valid bool
	}{
		{"minimal", "name: research\n", true},
		{
			"object forms",
			"name: research\n" +
				"description: coding agent\n" +
				"tools: [read, grep]\n" +
				"packages:\n  base: npm:@acme/base@1\n" +
				"extensions:\n  - name: subagent\n    tools: [ask]\n" +
				"skills:\n  - name: commit\n    origins: [local:skills/commit]\n" +
				"discovery:\n  extensions: [workspace]\n  skills: [workspace, user]\n" +
				"systemPrompt:\n  text: hi\n" +
				"model:\n  provider: openai\n  name: gpt-5\n" +
				"build:\n  targets: [linux/amd64]\n  outputName: pig-research\n" +
				"release:\n  version: 1.2.0\n",
			true,
		},
		{
			"shorthand forms",
			"name: research\n" +
				"extensions:\n  - subagent\n" +
				"skills:\n  - commit\n",
			true,
		},
		{
			"agentEnv image",
			"name: dev\nagentEnv:\n  image: ghcr.io/acme/dev:1\n  policy:\n    preset: elevated\n  mounts:\n    - source: /host\n      target: /work\n",
			true,
		},
		{"typed secrets", "name: secrets\nsecrets:\n  - name: token\n    from: {env: TOKEN}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: token\n      target: {env: TOKEN}\n", true},
		{"removed MCP transport", "name: secrets\nmcpServers:\n  s:\n    command: server\n", false},
		{"multiple secret source arms", "name: secrets\nsecrets:\n  - name: token\n    from: {env: TOKEN, ref: x:y}\n", false},
		{"typed extends", "name: child\nextends:\n  source: local:./base.yaml\n  version: ^1.0\n  remove:\n    skills: [old]\n", true},
		{"removed string extends", "name: child\nextends: base\n", false},
		{"remove without extends", "name: child\nremove:\n  skills: [old]\n", false},
		{"null is not deletion", "name: child\nmodel: null\n", false},
		{"missing name", "", false},
		{"wrong version", "version: 2\nname: x\n", false},
		{"version wrong type", "version: one\nname: x\n", false},
		{"unknown top field", "name: x\nbogus: true\n", false},
		{"unknown build field", "name: x\nbuild:\n  tier: fuse\n", false},
		{"unknown nested field", "name: x\nmodel:\n  bogus: y\n", false},
		{"removed MCP field", "name: x\nmcpServers:\n  s:\n    bogus: y\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schemaErr := ValidateAgainstSchema([]byte(tc.yaml))
			_, goErr := ParseBytes([]byte(tc.yaml))
			schemaAccepts := schemaErr == nil
			goAccepts := goErr == nil
			if goAccepts != tc.valid {
				t.Fatalf("Go accepts=%v, want valid=%v (err=%v)", goAccepts, tc.valid, goErr)
			}
			if schemaAccepts != goAccepts {
				t.Fatalf("vocabulary disagreement: schema accepts=%v (%v), Go accepts=%v (%v)", schemaAccepts, schemaErr, goAccepts, goErr)
			}
		})
	}
}

// TestAC1SchemaIsPublishedAndClosed guards the published schema surface: it is
// non-empty, valid JSON, and closes the top-level vocabulary.
func TestAC1SchemaIsPublishedAndClosed(t *testing.T) {
	data := SchemaJSON()
	if len(data) == 0 {
		t.Fatal("SchemaJSON() is empty")
	}
	if !strings.Contains(string(data), "\"additionalProperties\": false") {
		t.Fatal("published schema does not close its vocabulary")
	}
	if err := ValidateAgainstSchema([]byte("name: ok\n")); err != nil {
		t.Fatalf("published schema rejects a minimal valid Piglet: %v", err)
	}
}

// TestPigletSchemaCommandPublishesValidJSON pins the `pig piglet schema`
// publish surface: it prints the schema as valid JSON on stdout with a clean
// stderr and a zero exit.
func TestPigletSchemaCommandPublishesValidJSON(t *testing.T) {
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "schema"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &document); err != nil {
		t.Fatalf("schema output is not valid JSON: %v", err)
	}
	if document["$id"] != "https://pig.dev/schemas/piglet.json" {
		t.Fatalf("unexpected schema $id: %v", document["$id"])
	}
}
