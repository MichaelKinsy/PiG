package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// execute.ts appends `{ set, delete }` with set a plain object the host filled key by key (host.ts parseStoreWrites), so JSON.stringify lists the array-index keys
// ascending and then the other keys in the order the script first wrote them. The pinned Pi's executeCodemode and Execute run the same store() sequences with an
// empty session branch; the data of the codemode-store entry each appends must be equal, byte for byte.
func TestStoreEntryKeyOrderMatchesPi(t *testing.T) {
	keys := []string{"b", "a", "z", "10", "2", "1", "0", "01", "-1", "4294967294", "4294967295", "1.5", "x y", "é", "constructor", "toString"}
	rng := rand.New(rand.NewPCG(13, 17))
	scripts := []string{`store("b", 1); store("a", 2)`, `store("10", 1); store("2", 2); store("a", 3)`, `store("b", 1); store("a", 2); store("b", 3)`, `store("a", 1); store("a", undefined); store("b", 2)`}
	for range 150 {
		var calls []string
		for range 1 + rng.IntN(6) {
			value := fmt.Sprint(rng.IntN(100))
			if rng.IntN(5) == 0 {
				value = "undefined"
			}
			calls = append(calls, fmt.Sprintf("store(%q, %s)", keys[rng.IntN(len(keys))], value))
		}
		scripts = append(scripts, strings.Join(calls, "; "))
	}
	cache := t.TempDir()
	wasm, err := filepath.Abs("../../../../codemode/assets/quickjs.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cache, "quickjs-wasi"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, wasm, filepath.Join(cache, "quickjs-wasi", "quickjs.wasm"))
	scriptPath, err := filepath.Abs("testdata/store_entry_oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	var want [][]entry
	pioracle.Run(t, `
const { spawnSync } = await import("node:child_process");
const child = spawnSync(process.execPath, [process.env.ORACLE_SCRIPT], { input: JSON.stringify(input), encoding: "utf8", maxBuffer: 1 << 28, env: { ...process.env, NODE_PATH: process.env.ORACLE_NODE_PATH } });
if (child.status !== 0) throw new Error(child.stderr);
emit(JSON.parse(child.stdout));
`, scripts, &want, "ORACLE_SCRIPT="+scriptPath, "ORACLE_NODE_PATH="+cache)
	bad := 0
	for i, script := range scripts {
		// A VM keeps its slot until it has exited, after Execute has returned, and a script that finds two taken waits for one, the wait counting toward its timeout_ms (the pinned Pi's oracle
		// above waits for the exits of its workers for the same reason), so a script starts once the earlier ones' VMs have exited.
		codemodeWorkers.waitIdle()
		var got []entry
		base := extension.NewContext(t.TempDir(), nil, func() error { return nil }, extension.ContextActions{SessionManager: emptyBranch{}})
		ctx := extension.WithToolContext(context.Background(), extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{
			AppendEntry: func(customType string, data any) error {
				encoded, err := json.Marshal(data)
				if err != nil {
					return err
				}
				got = append(got, entry{customType, string(encoded)})
				return nil
			},
		}))
		params, _ := json.Marshal(map[string]string{"code": script})
		if _, err := Execute(ctx, "call", params, nil, Options{}); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want[i]) {
			if bad++; bad <= 6 {
				t.Errorf("%s\n  PiG %v\n  Pi  %v", script, got, want[i])
			}
		}
	}
	if bad > 6 {
		t.Errorf("%d of %d scripts differ", bad, len(scripts))
	}
}
