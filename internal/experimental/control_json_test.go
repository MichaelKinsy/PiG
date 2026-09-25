package experimental

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEncodeControlLineExactUpstreamJSON(t *testing.T) {
	values := []string{
		`null`, `true`, `false`, `[]`, `{}`,
		`[9007199254740993,-0,1e400,-1e400,1e-400,1e-7,1e-6,1e20,1e21,0.00000101]`,
		`{"b":1,"3":3,"2":2,"01":4,"4294967294":5,"4294967295":6,"a":1,"b":2}`,
		`["\u0000\u0001\u0008\u0009\u000a\u000c\u000d\u001f","\u0022\u005c\/","\\u2028\u2028\u2029"]`,
		`["\ud800","\udc00","\ud83d\ude00","\ud800\u0041","\udc00\ud800","\uD800"]`,
		`{"\ud800":1,"\udc00":2,"\u0061":3,"a":4,"nested":[{"2":["é😀",null],"1":false}]}`,
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--import", upstreamLoaderImport(root), filepath.Join(root, "internal/experimental/testdata/process-json-oracle.mjs"))
	cmd.Env = append(os.Environ(), "PIG_TEST_ROOT="+root, "HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader(strings.Join(values, "\n") + "\n")
	want, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upstream JSON oracle: %v\n%s", err, want)
	}
	var got strings.Builder
	got.WriteString(strconv.Itoa(MaxControlLineBytes) + "\n")
	for _, value := range values {
		line, err := EncodeControlLine(json.RawMessage(value))
		if err != nil {
			t.Fatal(err)
		}
		got.WriteString(line)
	}
	if got.String() != string(want) {
		t.Fatalf("native:\n%s\nupstream:\n%s", got.String(), want)
	}
}
