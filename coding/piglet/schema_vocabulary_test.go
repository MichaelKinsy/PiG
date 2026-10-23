package piglet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	agenttools "github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// The published JSON Schema is a hand-maintained mirror of the Go parser, and ValidateAgainstSchema has no caller on the
// validation path, so nothing but these tests notices when the two drift. A builder that trusts the schema refuses a
// Piglet the parser accepts: the schema once listed seven built-in tools while the parser accepted eight, and rejected
// `powershell`. Each test derives the parser's side from code, never from a copy of the schema.

func schemaDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(SchemaJSON(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// schemaNode follows a JSON pointer such as "$defs/discovery/properties/skills" below the schema root.
func schemaNode(t *testing.T, document map[string]any, pointer string) map[string]any {
	t.Helper()
	var node any = document
	for part := range strings.SplitSeq(pointer, "/") {
		object, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("schema %q: %q is not an object", pointer, part)
		}
		if next, found := object[part]; found {
			node = next
			continue
		}
		// A step through a $ref'd definition.
		ref, _ := object["$ref"].(string)
		target, found := strings.CutPrefix(ref, "#/")
		if !found {
			t.Fatalf("schema %q has no %q", pointer, part)
		}
		resolved := schemaNode(t, document, target)
		next, found := resolved[part]
		if !found {
			t.Fatalf("schema %q has no %q", pointer, part)
		}
		node = next
	}
	result, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("schema %q is not an object", pointer)
	}
	return result
}

// schemaProperties collects the property names of a schema node, including the ones it takes through $ref and through
// the object arms of oneOf.
func schemaProperties(t *testing.T, document map[string]any, node map[string]any) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	var walk func(map[string]any)
	walk = func(node map[string]any) {
		if properties, ok := node["properties"].(map[string]any); ok {
			for name := range properties {
				names[name] = true
			}
		}
		if ref, ok := node["$ref"].(string); ok {
			target, _ := strings.CutPrefix(ref, "#/")
			walk(schemaNode(t, document, target))
		}
		if arms, ok := node["oneOf"].([]any); ok {
			for _, arm := range arms {
				if object, ok := arm.(map[string]any); ok && object["type"] == "object" {
					walk(object)
				}
			}
		}
	}
	walk(node)
	return names
}

func schemaRequired(node map[string]any) map[string]bool {
	required := map[string]bool{}
	list, _ := node["required"].([]any)
	for _, name := range list {
		required[name.(string)] = true
	}
	return required
}

// entryObject is the object arm of a string-or-object entry.
func entryObject(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	for _, arm := range node["oneOf"].([]any) {
		if object, ok := arm.(map[string]any); ok && object["type"] == "object" {
			return object
		}
	}
	t.Fatalf("schema node %v has no object arm", node)
	return nil
}

func enumOf(t *testing.T, node map[string]any) []string {
	t.Helper()
	list, ok := node["enum"].([]any)
	if !ok {
		t.Fatalf("schema node %v has no enum", node)
	}
	values := make([]string, len(list))
	for i, value := range list {
		values[i] = value.(string)
	}
	return values
}

// goFields reads a Go type's YAML keys and which of them have no omitempty.
func goFields(typ reflect.Type) (keys []string, mandatory map[string]bool) {
	mandatory = map[string]bool{}
	for field := range typ.Fields() {
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		keys = append(keys, name)
		if !strings.Contains(options, "omitempty") {
			mandatory[name] = true
		}
	}
	slices.Sort(keys)
	return keys, mandatory
}

// TestSchemaFieldsMatchParserFields compares the keys each schema object allows with the YAML keys of the Go type the
// parser decodes it into, and requires every key the Go type always writes to be required by the schema.
func TestSchemaFieldsMatchParserFields(t *testing.T) {
	document := schemaDocument(t)
	for _, tc := range []struct {
		typ     reflect.Type
		pointer string // below the schema root; "" is the root
		entry   bool   // a string-or-object entry
	}{
		{reflect.TypeFor[Piglet](), "", false},
		{reflect.TypeFor[ExtendsSpec](), "$defs/extendsSpec", false},
		{reflect.TypeFor[RemoveSpec](), "$defs/removeSpec", false},
		{reflect.TypeFor[StripSpec](), "$defs/stripSpec", false},
		{reflect.TypeFor[StripKeep](), "$defs/stripLists", false},
		{reflect.TypeFor[Slots](), "$defs/slots", false},
		{reflect.TypeFor[SlotMember](), "$defs/slotMember", false},
		{reflect.TypeFor[SecretDeclaration](), "$defs/secretDeclaration", false},
		{reflect.TypeFor[SecretSource](), "$defs/secretSource", false},
		{reflect.TypeFor[ExtensionEntry](), "$defs/extensionEntry", true},
		{reflect.TypeFor[SkillEntry](), "$defs/skillEntry", true},
		{reflect.TypeFor[Discovery](), "$defs/discovery", false},
		{reflect.TypeFor[PromptRef](), "$defs/promptRef", false},
		{reflect.TypeFor[AgentEnvironment](), "$defs/agentEnvironment", false},
		{reflect.TypeFor[AgentPigRuntime](), "$defs/agentPigRuntime", false},
		{reflect.TypeFor[AgentEnvironmentPolicy](), "$defs/agentEnvironmentPolicy", false},
		{reflect.TypeFor[AgentWorkspace](), "$defs/agentWorkspace", false},
		{reflect.TypeFor[AgentMount](), "$defs/agentMount", false},
		{reflect.TypeFor[AgentSecretBinding](), "$defs/agentSecretBinding", false},
		{reflect.TypeFor[AgentSecretTarget](), "$defs/agentSecretBinding/properties/target", false},
		{reflect.TypeFor[ModelConfig](), "$defs/modelConfig", false},
		{reflect.TypeFor[BuildSpec](), "$defs/buildSpec", false},
		{reflect.TypeFor[ReleaseSpec](), "$defs/releaseSpec", false},
	} {
		t.Run(tc.typ.Name(), func(t *testing.T) {
			node := document
			if tc.pointer != "" {
				node = schemaNode(t, document, tc.pointer)
			}
			if tc.entry {
				node = entryObject(t, node)
			}
			got := schemaProperties(t, document, node)
			want, mandatory := goFields(tc.typ)
			if tc.typ == reflect.TypeFor[AgentSecretTarget]() {
				mandatory = map[string]bool{"env": true} // validateAgentEnvironment refuses an empty target.env.
			}
			for _, key := range want {
				if !got[key] {
					t.Errorf("the schema rejects the key %q that the parser accepts", key)
				}
			}
			for key := range got {
				if !slices.Contains(want, key) {
					t.Errorf("the schema accepts the key %q that the parser rejects", key)
				}
			}
			required := schemaRequired(node)
			for key := range mandatory {
				if !required[key] {
					t.Errorf("the parser always reads %q but the schema does not require it", key)
				}
			}
		})
	}
}

// TestSchemaEnumsMatchParserSets compares each enum of the schema with the set the parser accepts, read from the code
// that owns the set.
func TestSchemaEnumsMatchParserSets(t *testing.T) {
	document := schemaDocument(t)
	for _, tc := range []struct {
		name    string
		pointer string
		want    []string
	}{
		{"tools", "properties/tools/items", agenttools.BuiltinToolNames()},
		{"extends.remove.slots", "$defs/removeSpec/properties/slots/items", slotIDs},
		{"discovery.extensions", "$defs/discovery/properties/extensions/items", discoverySources},
		{"discovery.skills", "$defs/discovery/properties/skills/items", discoverySources},
		{"agentEnv.pigRuntime.mode", "$defs/agentPigRuntime/properties/mode", agentRuntimeModes},
		{"agentEnv.policy.preset", "$defs/agentEnvironmentPolicy/properties/preset", agentPolicyPresets},
		{"build.extensionRealization", "$defs/buildSpec/properties/extensionRealization", extensionRealizations},
		{"strip.tools", "$defs/stripToolId", pigstrip.Known(pigstrip.ListTools)},
		{"strip.commands", "$defs/stripCommandId", pigstrip.Known(pigstrip.ListCommands)},
		{"strip.extensions", "$defs/stripExtensionId", pigstrip.Known(pigstrip.ListExtensions)},
		{"strip.apis", "$defs/stripApiId", pigstrip.Known(pigstrip.ListAPIs)},
		{"strip.features", "$defs/stripFeatureId", pigstrip.Known(pigstrip.ListFeatures)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := enumOf(t, schemaNode(t, document, tc.pointer))
			if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(tc.want))) {
				t.Fatalf("schema enum = %v\nparser set  = %v", got, tc.want)
			}
		})
	}
	// Each strip list takes its IDs from its own def.
	properties := schemaNode(t, document, "$defs/stripListProperties/properties")
	for list, def := range map[string]string{
		pigstrip.ListTools: "stripToolId", pigstrip.ListCommands: "stripCommandId", pigstrip.ListExtensions: "stripExtensionId",
		pigstrip.ListAPIs: "stripApiId", pigstrip.ListFeatures: "stripFeatureId",
	} {
		items := properties[list].(map[string]any)["items"].(map[string]any)
		if items["$ref"] != "#/$defs/"+def {
			t.Errorf("strip.%s items = %v, want a $ref to %s", list, items, def)
		}
	}
}

func yamlScalar(value string) string { return strconv.Quote(value) }

// accepted reports whether the parser and the schema accept or reject doc alike.
func accepted(doc string) (parser, schema bool, parserErr, schemaErr error) {
	_, parserErr = ParseBytes([]byte(doc))
	schemaErr = ValidateAgainstSchema([]byte(doc))
	return parserErr == nil, schemaErr == nil, parserErr, schemaErr
}

// TestSchemaAgreesWithParserOnEveryValueOfASet feeds each member of every closed set, the members of the other side's
// set, and values outside both through the parser and the schema. Powershell was the member the schema lacked.
func TestSchemaAgreesWithParserOnEveryValueOfASet(t *testing.T) {
	document := schemaDocument(t)
	type location struct {
		name     string
		template string // %s is a YAML scalar
		parser   []string
		pointers []string
	}
	for _, tc := range []location{
		{"tools", "name: x\ntools: [%s]\n", agenttools.BuiltinToolNames(), []string{"properties/tools/items"}},
		{"discovery.extensions", "name: x\ndiscovery:\n  extensions: [%s]\n", discoverySources, []string{"$defs/discovery/properties/extensions/items"}},
		{"discovery.skills", "name: x\ndiscovery:\n  skills: [%s]\n", discoverySources, []string{"$defs/discovery/properties/skills/items"}},
		{"extends.remove.slots", "name: x\nextends:\n  source: local:./base.yaml\n  remove:\n    slots: [%s]\n", slotIDs, []string{"$defs/removeSpec/properties/slots/items"}},
		{"agentEnv.pigRuntime.mode", "name: x\nagentEnv:\n  image: dev:1\n  pigRuntime:\n    mode: %s\n", agentRuntimeModes, []string{"$defs/agentPigRuntime/properties/mode"}},
		{"agentEnv.policy.preset", "name: x\nagentEnv:\n  image: dev:1\n  policy:\n    preset: %s\n", agentPolicyPresets, []string{"$defs/agentEnvironmentPolicy/properties/preset"}},
		{"build.extensionRealization", "name: x\nbuild:\n  extensionRealization: %s\n", extensionRealizations, []string{"$defs/buildSpec/properties/extensionRealization"}},
		{"strip.tools", "name: x\nstrip:\n  tools: [%s]\n", pigstrip.Known(pigstrip.ListTools), []string{"$defs/stripToolId"}},
		{"strip.commands", "name: x\nstrip:\n  commands: [%s]\n", pigstrip.Known(pigstrip.ListCommands), []string{"$defs/stripCommandId"}},
		{"strip.extensions", "name: x\nstrip:\n  extensions: [%s]\n", pigstrip.Known(pigstrip.ListExtensions), []string{"$defs/stripExtensionId"}},
		{"strip.apis", "name: x\nstrip:\n  apis: [%s]\n", pigstrip.Known(pigstrip.ListAPIs), []string{"$defs/stripApiId"}},
		{"strip.features", "name: x\nstrip:\n  features: [%s]\n", pigstrip.Known(pigstrip.ListFeatures), []string{"$defs/stripFeatureId"}},
		{"strip.keep.tools", "name: x\nstrip:\n  keep:\n    tools: [%s]\n", pigstrip.Known(pigstrip.ListTools), []string{"$defs/stripToolId"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := slices.Clone(tc.parser)
			for _, pointer := range tc.pointers {
				candidates = append(candidates, enumOf(t, schemaNode(t, document, pointer))...)
			}
			candidates = append(candidates, "nope", "Read", " read")
			slices.Sort(candidates)
			for _, value := range slices.Compact(candidates) {
				doc := fmt.Sprintf(tc.template, yamlScalar(value))
				parser, schema, parserErr, schemaErr := accepted(doc)
				if parser != schema {
					t.Errorf("%s: parser accepts=%v (%v), schema accepts=%v (%v)", value, parser, parserErr, schema, schemaErr)
				}
			}
		})
	}
}

// TestSchemaNeverRejectsWhatTheParserAccepts pins the field-level rules the schema states beyond the closed sets:
// patterns, required fields and the agentEnv form. Every case is accepted or rejected by the parser; the schema must
// agree except for a rule only Go checks, which may only make the schema the more permissive side.
func TestSchemaNeverRejectsWhatTheParserAccepts(t *testing.T) {
	const elevated = "name: x\nagentEnv:\n  image: dev:1\n  policy:\n    preset: elevated\n"
	for _, tc := range []struct {
		name         string
		doc          string
		goOnlyChecks bool // the parser enforces more than the schema can state
	}{
		{"name with every allowed punctuation", "name: a.b_c-d\n", false},
		{"name with a leading hyphen", "name: -a\n", false},
		{"name with a space", "name: \"a b\"\n", false},
		{"name unicode", "name: ünï\n", false},
		{"package alias", "name: x\npackages:\n  a.b-c: npm:p@1\n", false},
		{"package alias leading dot", "name: x\npackages:\n  .a: npm:p@1\n", false},
		{"release 1.2.3", "name: x\nrelease:\n  version: 1.2.3\n", false},
		{"release prerelease", "name: x\nrelease:\n  version: 1.2.3-rc.1\n", false},
		{"release hyphenated prerelease", "name: x\nrelease:\n  version: 1.2.3-0a-b.7\n", false},
		{"release build metadata", "name: x\nrelease:\n  version: 1.2.3+build.05\n", false},
		{"release prerelease and build", "name: x\nrelease:\n  version: 1.2.3-alpha+001\n", false},
		{"release short minor", "name: x\nrelease:\n  version: \"1.2\"\n", false},
		{"release short major", "name: x\nrelease:\n  version: \"1\"\n", false},
		{"release short with prerelease", "name: x\nrelease:\n  version: 1.2-rc1\n", false},
		{"release v prefix", "name: x\nrelease:\n  version: v1.2.3\n", false},
		{"release leading zero", "name: x\nrelease:\n  version: 01.2.3\n", false},
		{"release numeric prerelease leading zero", "name: x\nrelease:\n  version: 1.2.3-01\n", false},
		{"release empty prerelease", "name: x\nrelease:\n  version: \"1.2.3-\"\n", false},
		{"release empty build", "name: x\nrelease:\n  version: \"1.2.3+\"\n", false},
		{"release four parts", "name: x\nrelease:\n  version: 1.2.3.4\n", false},
		{"release empty", "name: x\nrelease:\n  version: \"\"\n", false},
		{"build target", "name: x\nbuild:\n  targets: [linux/amd64, darwin/arm64, windows/arm64]\n", false},
		{"build target without arch", "name: x\nbuild:\n  targets: [linux]\n", false},
		{"build target upper case", "name: x\nbuild:\n  targets: [Linux/amd64]\n", false},
		{"build target with variant", "name: x\nbuild:\n  targets: [linux/amd64/v2]\n", false},
		{"build target duplicate", "name: x\nbuild:\n  targets: [linux/amd64, linux/amd64]\n", false},
		{"build outputName", "name: x\nbuild:\n  outputName: pig-x\n", false},
		{"build outputName dot", "name: x\nbuild:\n  outputName: .\n", false},
		{"build outputName dot dot", "name: x\nbuild:\n  outputName: ..\n", false},
		{"build outputName slash", "name: x\nbuild:\n  outputName: a/b\n", false},
		{"build outputName backslash", "name: x\nbuild:\n  outputName: \"a\\\\b\"\n", false},
		{"agentEnv empty", "name: x\nagentEnv: {}\n", false},
		{"agentEnv image", "name: x\nagentEnv:\n  image: dev:1\n", false},
		{"agentEnv image and source", "name: x\nagentEnv:\n  image: dev:1\n  source: npm:p@1\n", false},
		{"agentEnv blank image beside a source", "name: x\nagentEnv:\n  image: \"\"\n  source: npm:p@1\n", false},
		{"agentEnv blank image alone", "name: x\nagentEnv:\n  image: \" \"\n", false},
		{"agentEnv devContainer", "name: x\nagentEnv:\n  devContainer: workspace:.devcontainer/devcontainer.json\n", false},
		{"agentEnv devContainer and image", "name: x\nagentEnv:\n  image: dev:1\n  devContainer: workspace:.devcontainer/devcontainer.json\n", false},
		{"agentEnv image with a scheme", "name: x\nagentEnv:\n  image: https://dev:1\n", true},
		{"mount", elevated + "  mounts:\n    - source: /host\n      target: /work\n", false},
		{"mount without target", elevated + "  mounts:\n    - source: /host\n", false},
		{"mount without source", elevated + "  mounts:\n    - target: /work\n", false},
		{"mount with a relative target", elevated + "  mounts:\n    - source: /host\n      target: work\n", false},
		{"secret env", "name: x\nsecrets:\n  - name: t\n    from: {env: TOKEN_1}\n", false},
		{"secret env with a digit first", "name: x\nsecrets:\n  - name: t\n    from: {env: 1TOKEN}\n", false},
		{"secret env with a hyphen", "name: x\nsecrets:\n  - name: t\n    from: {env: A-B}\n", false},
		{"secret ref", "name: x\nsecrets:\n  - name: t\n    from: {ref: \"vault:kv/x\"}\n", false},
		{"secret ref without a resolver", "name: x\nsecrets:\n  - name: t\n    from: {ref: nocolon}\n", false},
		{"secret ref with a space", "name: x\nsecrets:\n  - name: t\n    from: {ref: \"vault:a b\"}\n", false},
		{"secret name with a space", "name: x\nsecrets:\n  - name: \"a b\"\n    from: {env: X}\n", false},
		{"secret binding target", "name: x\nsecrets:\n  - name: t\n    from: {env: X}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: t\n      target: {env: TOKEN}\n", false},
		{"secret binding target with a hyphen", "name: x\nsecrets:\n  - name: t\n    from: {env: X}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: t\n      target: {env: A-B}\n", false},
		{"secret binding without a target env", "name: x\nsecrets:\n  - name: t\n    from: {env: X}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: t\n      target: {}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parser, schema, parserErr, schemaErr := accepted(tc.doc)
			if parser && !schema {
				t.Fatalf("the schema rejects a Piglet the parser accepts: %v", schemaErr)
			}
			if !tc.goOnlyChecks && parser != schema {
				t.Fatalf("parser accepts=%v (%v), schema accepts=%v (%v)", parser, parserErr, schema, schemaErr)
			}
		})
	}
}

// TestSchemaAcceptsEveryPigletTheRepositoryShips runs the Piglet files of the repository through both sides: a Piglet the
// parser accepts must pass the schema.
func TestSchemaAcceptsEveryPigletTheRepositoryShips(t *testing.T) {
	root := filepath.Join("..", "..")
	var files []string
	for _, pattern := range []string{
		"piglets/*/*.yaml", "bench/evals/piglets/*.yaml", "coding/pigletbuild/binarypiglet/piglet.yaml", "coding/piglet/testdata/lineage/*.yaml",
	} {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) < 5 {
		t.Fatalf("found only %v; the Piglet files moved", files)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseBytes(data); err != nil {
			continue
		}
		if err := ValidateAgainstSchema(data); err != nil {
			t.Errorf("%s: the parser accepts it and the schema rejects it: %v", file, err)
		}
	}
}
