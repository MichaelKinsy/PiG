package ai

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// const, enum and uniqueItems compare JSON values: a number never equals a string, whichever comes first. The JSON Schema library's equality parsed the right-hand side of a numeric left-hand side with big.Rat, which accepts "1.5", "0x10" and "1/2",
// so a string const accepted the number 1 and uniqueItems rejected ["1", 1] (third_party/jsonschema/FORK.md item 6). Pi's validator, typebox 1.3.27, keeps them apart.
func TestValidateToolArgumentsComparesNumbersAndStringsLikePi(t *testing.T) {
	wrap := func(property string) string { return `{"type":"object","properties":{"a":` + property + `}}` }
	unique := wrap(`{"type":"array","uniqueItems":true}`)
	probes := []struct {
		Schema string `json:"schema"`
		Args   string `json:"args"`
	}{
		{wrap(`{"const":"1"}`), `{"a":1}`}, {wrap(`{"const":"1.5"}`), `{"a":1.5}`}, {wrap(`{"const":"0x10"}`), `{"a":16}`}, {wrap(`{"const":"1e3"}`), `{"a":1000}`}, {wrap(`{"const":"1"}`), `{"a":"1"}`},
		{wrap(`{"const":{"x":"1"}}`), `{"a":{"x":1}}`}, {wrap(`{"const":["1"]}`), `{"a":[1]}`}, {wrap(`{"const":["1"]}`), `{"a":["1"]}`}, {wrap(`{"const":1}`), `{"a":1}`}, {wrap(`{"const":1}`), `{"a":"1"}`},
		{wrap(`{"enum":["1","x"]}`), `{"a":1}`}, {wrap(`{"enum":["1.5","x"]}`), `{"a":1.5}`}, {wrap(`{"enum":[1,2]}`), `{"a":"1"}`}, {wrap(`{"enum":["1","x"]}`), `{"a":"1"}`},
		{unique, `{"a":["1.5",1.5]}`}, {unique, `{"a":[1.5,"1.5"]}`}, {unique, `{"a":["1",1]}`}, {unique, `{"a":[["1"],[1]]}`}, {unique, `{"a":[{"x":"1"},{"x":1}]}`}, {unique, `{"a":[{"x":1},{"x":"1"}]}`},
		{unique, `{"a":["0x10",16]}`}, {unique, `{"a":["1/2",0.5]}`}, {unique, `{"a":["",0]}`}, {unique, `{"a":[1,1]}`}, {unique, `{"a":["1","1"]}`}, {unique, `{"a":[1,2,"1"]}`}, {unique, `{"a":[1,"1",1.0]}`},
	}
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/validate_tool_args.mjs", testenv.ModuleRoot(t), pinnedTypeboxVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle (npm ci --ignore-scripts in internal/typebox/testdata/npm first): %v\n%s", err, &stderr)
	}
	var expected []validateResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	rejected := 0
	for i, probe := range probes {
		got, err := ValidateToolArgumentsJSON("probe", json.RawMessage(probe.Schema), json.RawMessage(probe.Args))
		have, want := string(got), strings.TrimSpace(string(expected[i].Ok))
		if err != nil {
			have = "ERR " + err.Error()
			rejected++
		}
		if expected[i].Err != nil {
			want = "ERR " + *expected[i].Err
		}
		if have != want {
			t.Errorf("probe %d: schema %s arguments %s\n  Pig %s\n  Pi  %s", i, probe.Schema, probe.Args, have, want)
		}
	}
	if rejected < 8 || rejected > len(probes)-8 {
		t.Errorf("probes are one-sided: %d of %d rejected", rejected, len(probes))
	}
}
