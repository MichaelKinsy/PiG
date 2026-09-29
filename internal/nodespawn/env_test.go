package nodespawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Node's normalizeSpawnArguments (lib/child_process.js) calls
// copyProcessEnvToEnv(env, 'NODE_V8_COVERAGE', options.env): a non-empty
// NODE_V8_COVERAGE in Pi's environment always reaches the child, unless the
// env option has a property of exactly that name, so a child started with
// an empty or allow-listed env still writes coverage. Node's spawn with the
// same env option is the oracle; the test binary reports the environment it
// received. Both environments are compared sorted, since libuv and os/exec
// order the block of non-ASCII names differently on Windows.
func TestSetEnvPropagatesNodeV8CoverageAsNodeDoes(t *testing.T) {
	helper := testenv.EnvironHelper + "=1"
	cases := []struct {
		name string
		set  bool
		env  []string
	}{
		{"the env is empty", true, []string{helper}},
		{"the env holds other variables", true, []string{helper, "PIG_A=1"}},
		{"the env sets its own value", true, []string{helper, "NODE_V8_COVERAGE=/own"}},
		{"the env sets an empty value", true, []string{helper, "NODE_V8_COVERAGE="}},
		{"the env names it in another case", true, []string{helper, "node_v8_coverage=/own"}},
		{"PiG's value is empty", false, []string{helper, "PIG_A=1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The Node oracle runs with the same value, in a directory it may write.
			if c.set {
				t.Setenv("NODE_V8_COVERAGE", t.TempDir())
			} else {
				t.Setenv("NODE_V8_COVERAGE", "")
			}
			requireSameChildEnvironment(t, c.env)
		})
	}
}

// requireSameChildEnvironment starts the test binary through Node's spawn and
// through SetEnv with env, and requires that it receives the same
// environment. Node's spawn is the oracle; the test binary reports the
// environment it received. Both are compared sorted, since libuv and os/exec
// order the block of non-ASCII names differently on Windows.
func requireSameChildEnvironment(t *testing.T, env []string) {
	t.Helper()
	requireSameChildEnvironmentFor(t, EnvProperties(env))
}

// orderedObject is the JSON of an object whose keys are properties, in order,
// where JavaScript keeps the order of names that are not array indexes.
func orderedObject(t *testing.T, properties []EnvProperty) string {
	t.Helper()
	var object strings.Builder
	object.WriteByte('{')
	for i, property := range properties {
		name, err := json.Marshal(property.Name)
		if err != nil {
			t.Fatal(err)
		}
		value, err := json.Marshal(property.Value)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			object.WriteByte(',')
		}
		object.Write(name)
		object.WriteByte(':')
		object.Write(value)
	}
	object.WriteByte('}')
	return object.String()
}

// requireSameChildEnvironmentFor is requireSameChildEnvironment for an env
// option whose properties are given, in order, to SetEnvProperties.
func requireSameChildEnvironmentFor(t *testing.T, properties []EnvProperty) {
	t.Helper()
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := json.Marshal(exe)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"file":` + string(file) + `,"env":` + orderedObject(t, properties) + `}`)
	node := exec.CommandContext(t.Context(), nodeBinary, "-e", `
const { spawnSync } = require("node:child_process");
const { file, env } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync(file, [], { env, encoding: "utf8" });
if (result.error || result.status !== 0) throw result.error ?? new Error(result.stderr);
process.stdout.write(result.stdout);
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	cmd := exec.Command(exe)
	SetEnvProperties(cmd, properties)
	SetProgram(cmd)
	SetCommandLine(cmd)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v; output %s", err, output)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("child environment\n got %q\nwant %q", got, want)
	}
}

// A property name may contain "=", and libuv writes it into the block as
// "name=value" with the first "=" of the entry ending no name. Two properties
// whose entries share the text before the first "=" both reach the child, as
// they do in Node; os/exec alone keeps only the last of them. The test binary
// reports what its getenv sees, which is the first of such entries.
func TestSetEnvPropertiesPassesAPropertyNameWithAnEqualsSign(t *testing.T) {
	helper := EnvProperty{testenv.EnvironHelper, "1"}
	for _, c := range []struct {
		name string
		env  []EnvProperty
	}{
		{"no other name shares its text", []EnvProperty{helper, {"PIG_A=B", "two"}}},
		{"a name shares the text before its first equals sign", []EnvProperty{helper, {"PIG_A", "one"}, {"PIG_A=B", "two"}}},
		{"the longer name comes first", []EnvProperty{helper, {"PIG_A=B", "two"}, {"PIG_A", "one"}}},
		{"three names share the text", []EnvProperty{helper, {"PIG_A", "one"}, {"PIG_A=B", "two"}, {"PIG_A=B=C", "three"}, {"PIG_Z", "z"}}},
		{"a value holds non-ASCII text", []EnvProperty{helper, {"PIG_A", "\u00e9"}, {"PIG_A=B", "x y"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			requireSameChildEnvironmentFor(t, c.env)
		})
	}
}

// SetEnv describes a property by an entry, which ends its name at the first
// "=", so an entry for a name that another entry starts replaces it as an
// object property assignment does.
func TestSetEnvEntryEndsItsNameAtTheFirstEqualsSign(t *testing.T) {
	helper := testenv.EnvironHelper + "=1"
	requireSameChildEnvironment(t, []string{helper, "PIG_A=one", "PIG_A=B=two"})
}
