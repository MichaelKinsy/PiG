package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/tools/bash.ts

// The bash tool against pinned Pi over seeded commands (output on stdout and stderr, exit codes, no output, output past the line and byte limits,
// multi-byte and invalid UTF-8 output, CRLF, ANSI, a timeout): the result text, its details and the error text. The directory a probe runs in
// and the path of the full output file differ between runs and are masked.
func TestBashToolMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(173))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	type probe struct {
		Args map[string]any `json:"args"`
	}
	var probes []probe
	for range 200 {
		var command string
		switch rng.Intn(14) {
		case 0:
			command = "echo " + pick("hello", "'a b'", "é日本🙂", `"$PWD" | wc -l`)
		case 1:
			command = "echo out; echo err >&2; exit " + pick("0", "1", "3", "127")
		case 2:
			command = "true"
		case 3:
			command = "seq 1 " + pick("10", "2000", "2001", "5000", "100000")
		case 4:
			command = "head -c " + pick("51200", "51201", "60000", "200000") + " /dev/zero | tr '\\0' 'x'"
		case 5:
			command = "printf '%s\\r\\n' a b c; printf 'no newline'"
		case 6:
			command = `printf '\033[31mred\033[0m\n'`
		case 7:
			command = `printf 'ok\xff\xfe bad\n'`
		case 8:
			command = "echo before; exit " + pick("2", "255")
		case 9:
			command = "yes 'long line of text 0123456789' | head -n " + pick("1500", "3000")
		case 10:
			command = "nonexistent_command_xyz"
		case 11:
			command = "printf 'x%.0s' $(seq 1 " + pick("1000", "70000") + ")"
		case 12:
			command = "echo start"
			if rng.Intn(3) == 0 {
				command = "echo start; sleep 5; echo never"
			}
		default:
			command = "echo a && echo b >&2 && false"
		}
		args := map[string]any{"command": command}
		if strings.Contains(command, "sleep 5") {
			args["timeout"] = float64(1)
		} else if rng.Intn(10) == 0 {
			args["timeout"] = float64(10)
		}
		probes = append(probes, probe{Args: args})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bash_tool.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Text    string         `json:"text"`
		Details map[string]any `json:"details"`
		Thrown  string         `json:"thrown"`
		Dir     string         `json:"dir"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	mask := func(text string, details map[string]any, dir string) []byte {
		text = strings.ReplaceAll(text, dir, "<cwd>")
		for _, key := range []string{"fullOutputPath"} {
			if value, ok := details[key].(string); ok {
				text = strings.ReplaceAll(text, value, "<full>")
				details[key] = "<full>"
			}
		}
		encoded, _ := json.Marshal([]any{text, details})
		return encoded
	}
	failures, ok := 0, 0
	for i, p := range probes {
		dir := t.TempDir()
		tool := CreateBashTool(dir, nil)
		args, _ := json.Marshal(p.Args)
		result, execErr := tool.Execute(context.Background(), "", args, nil)
		var text string
		var details map[string]any
		switch {
		case execErr != nil:
			text = execErr.Error()
		case result.IsError:
			text = result.Text()
		default:
			text = result.Text()
			raw, _ := json.Marshal(result.Details)
			_ = json.Unmarshal(raw, &details)
		}
		if want[i].Thrown == "" {
			ok++
		}
		expectedText := want[i].Thrown + want[i].Text
		if details == nil {
			details = map[string]any{}
		}
		if want[i].Details == nil {
			want[i].Details = map[string]any{}
		}
		got, expected := mask(text, details, dir), mask(expectedText, want[i].Details, want[i].Dir)
		if !bytes.Equal(got, expected) {
			if failures++; failures <= 5 {
				t.Errorf("probe %d: %s\n%s", i, args, firstDiffOf(got, expected))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if ok < 60 {
		t.Fatalf("Pi's bash tool succeeded in only %d of %d probes", ok, len(probes))
	}
}
