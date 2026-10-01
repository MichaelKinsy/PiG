package nodespawn

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Node's normalizeSpawnArguments (lib/child_process.js) calls
// copyProcessEnvToEnv(env, 'NODE_V8_COVERAGE', options.env): a non-empty
// NODE_V8_COVERAGE in Pi's environment always reaches the child, unless the
// env option has a property of exactly that name, so a child started with
// an empty or allow-listed env still writes coverage. Node's spawn with the
// same env option is the oracle; the test binary reports the environment it
// received, in block order.
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
// through SetEnv and Start with env, and requires that it receives the same
// environment. Node's spawn is the oracle; the test binary reports the
// environment it received. The block of names outside ASCII is compared
// sorted, since os/exec can order it differently from libuv on Windows
// (SetEnvProperties).
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

// output starts cmd with Start and returns what it writes to stdout, as
// cmd.Output does.
func output(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	cmd.Stdout = write
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	out, readErr := io.ReadAll(read)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%v; output %s", err, out)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return out
}

// requireSameChildEnvironmentFor is requireSameChildEnvironment for an env
// option whose properties are given, in order, to SetEnvProperties. Outside
// Windows the environments must be equal in block order, the env option's. On
// Windows they must hold the same entries, the entries whose names are ASCII
// must be in the same order, which is libuv's, and each name must read the
// same value, which is that of the first entry for it; os/exec orders the
// names outside ASCII differently (SetEnvProperties).
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
	stdout := output(t, cmd)
	var got []string
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if !caseInsensitiveEnv {
		if !slices.Equal(got, want) {
			t.Errorf("child environment in block order\n got %q\nwant %q", got, want)
		}
		return
	}
	asciiNamed := func(block []string) []string {
		return slices.DeleteFunc(slices.Clone(block), func(entry string) bool {
			name, _ := envName(entry)
			return !isASCII(name)
		})
	}
	if gotASCII, wantASCII := asciiNamed(got), asciiNamed(want); !slices.Equal(gotASCII, wantASCII) {
		t.Errorf("child environment's ASCII names in block order\n got %q\nwant %q", gotASCII, wantASCII)
	}
	if gotView, wantView := getenvView(got), getenvView(want); !maps.Equal(gotView, wantView) {
		t.Errorf("child's getenv\n got %q\nwant %q", gotView, wantView)
	}
	got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("child environment\n got %q\nwant %q", got, want)
	}
}

// getenvView is the value that Windows' GetEnvironmentVariableW reads for each
// name of block: that of the first entry for the name, compared regardless of
// case.
func getenvView(block []string) map[string]string {
	view := make(map[string]string, len(block))
	for _, entry := range block {
		name, value := envName(entry)
		if _, ok := view[strings.ToUpper(name)]; !ok {
			view[strings.ToUpper(name)] = value
		}
	}
	return view
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// A property name may contain "=", and libuv writes it into the block as
// "name=value" with the first "=" of the entry ending no name. Two properties
// whose entries share the text before the first "=" both reach the child, as
// they do in Node; os/exec alone keeps only the last of them. Outside Windows
// the test binary's os.Environ keeps the first of such entries, which its
// getenv sees. On Windows it reports every entry in block order, where libuv's
// qsort orders the entries whose names compare equal; a block of more than
// eight entries takes qsort's partitioning path.
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
		{"many names share the text", []EnvProperty{
			helper, {"PIG_M", "m"}, {"PIG_A=5", "five"}, {"pig_a=2", "two"}, {"PIG_A", "zero"}, {"PIG_B", "b"},
			{"PIG_A=3", "three"}, {"Pig_A=1", "one"}, {"PIG_C", "c"}, {"PIG_A=4", "four"}, {"PIG_D", "d"}, {"PIG_E", "e"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			requireSameChildEnvironmentFor(t, c.env)
		})
	}
}

// Node keeps both of two names whose toUpperCase values differ, such as
// "PIG_K" and "PIG_\u212a" (KELVIN SIGN, which has no uppercase mapping), and
// libuv passes both. os/exec on Windows would keep only the last of them,
// because strings.ToLower maps both to "pig_k".
func TestSetEnvPropertiesPassesNamesThatOnlyLowercaseAlike(t *testing.T) {
	helper := EnvProperty{testenv.EnvironHelper, "1"}
	requireSameChildEnvironmentFor(t, []EnvProperty{helper, {"PIG_K", "letter"}, {"PIG_\u212a", "kelvin"}})
}

// libuv orders PIG_\u00e9 (U+00E9) before PIG_\u00ca (U+00CA), since
// CompareStringOrdinal uppercases \u00e9 to U+00C9, and PIG_\U0001F600 before
// PIG_\uff21 by UTF-16 code unit. os/exec's order compares UTF-8 bytes, with
// only ASCII letters uppercased, and puts both pairs the other way round. The
// entries whose names are ASCII, such as the ones that share the name PIG_A,
// must still reach the child in libuv's order, so that the child's getenv of
// PIG_A reads the value Node's child reads.
func TestSetEnvPropertiesKeepsLibuvOrderBesideNamesOutsideASCII(t *testing.T) {
	helper := EnvProperty{testenv.EnvironHelper, "1"}
	shared := func(n int) []EnvProperty {
		properties := []EnvProperty{{"PIG_A", "0"}}
		for i := 1; i <= n; i++ {
			properties = append(properties, EnvProperty{"PIG_A=" + strconv.Itoa(i), strconv.Itoa(i)})
		}
		return properties
	}
	for _, c := range []struct {
		name string
		env  []EnvProperty
	}{
		{"a Latin-1 pair", append([]EnvProperty{helper, {"PIG_\u00e9", "e"}, {"PIG_\u00ca", "E"}}, shared(9)...)},
		{"a surrogate pair and a fullwidth letter", append([]EnvProperty{helper, {"PIG_\U0001F600", "smile"}, {"PIG_\uff21", "fullwidth"}}, shared(9)...)},
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
