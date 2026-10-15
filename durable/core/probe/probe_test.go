// SPDX-License-Identifier: MIT

package probe

import (
	"bytes"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/abitest"
)

var update = flag.Bool("update", false, "rewrite the .steps golden files")

// TestScripts runs every testdata/*.events script through the native Step and compares the encoded steps with the
// checked-in golden file. The shim's Wasm conformance test replays the same scripts through the probe module and
// compares the same bytes (ABI section 12, binding conformance).
func TestScripts(t *testing.T) {
	scripts, _ := filepath.Glob("testdata/*.events")
	if len(scripts) == 0 {
		t.Fatal("no scripts")
	}
	for _, script := range scripts {
		t.Run(filepath.Base(script), func(t *testing.T) {
			f, err := os.Open(script)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			events, err := abitest.ParseScript(f)
			if err != nil {
				t.Fatal(err)
			}
			steps := abitest.RunScript(NewSession(func() string { return "01234567-89ab-7cde-8f01-23456789abcd" }), events)
			var got strings.Builder
			for _, s := range steps {
				got.WriteString(hex.EncodeToString(s))
				got.WriteByte('\n')
			}
			golden := strings.TrimSuffix(script, ".events") + ".steps"
			if *update {
				if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != string(want) {
				t.Fatalf("steps differ from %s; run go test -update if the change is intended", golden)
			}
			for i, s := range steps {
				decoded, err := abi.DecodeStep(s)
				if err != nil {
					t.Fatalf("step %d does not decode: %v", i, err)
				}
				if again := abi.AppendStep(nil, decoded); !bytes.Equal(again, s) {
					t.Fatalf("step %d does not re-encode to the same bytes", i)
				}
			}
		})
	}
}

func TestSQLTableAndGuard(t *testing.T) {
	var guards int
	for _, q := range SQL {
		if strings.HasPrefix(q, "UPDATE durable_metadata") && strings.Contains(q, "AND next_seq = ?") {
			guards++
		}
	}
	if guards != 1 {
		t.Fatalf("want one guarded durable_metadata update, got %d", guards)
	}
}
