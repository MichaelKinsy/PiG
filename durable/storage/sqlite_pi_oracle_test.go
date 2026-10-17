package storage_test

// pi: packages/durable/src/storage/sqlite/storage.ts (via node.ts, Node's built-in node:sqlite)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func runPigSQLite(t *testing.T, programs []oracleProgram, dirs []string, readsOnly bool) [][]oracleResult {
	t.Helper()
	out := make([][]oracleResult, len(programs))
	for i, program := range programs {
		store, err := sqlitenode.OpenNodeSqliteStorage(filepath.Join(dirs[i], "store.db"), sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			out[i] = []oracleResult{{Index: -1, Err: &struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			}{"Error", err.Error()}}}
			continue
		}
		out[i] = runPigProgram(t, program, store, readsOnly)
		if err := store.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// Pi's SQLite storage and Pig's run the same seeded programs on their own database files, then each opens the other's file: the answers, and what a reopened storage reads back, must agree, so the two implementations share one schema and one row encoding.
func TestSqliteStorageMatchesPiOnSeededPrograms(t *testing.T) {
	const seeded = 150
	programs := make([]oracleProgram, seeded)
	for i := range programs {
		programs[i] = oracleProgramFor(int64(20264040+i), false)
	}
	programs = append(programs, requestKeyPrograms()...)
	programs = append(programs, documentCopyPrograms()...)
	programs = append(programs, documentBoundaryPrograms()...)
	programs = append(programs, danglingParentPrograms()...)
	programs = append(programs, statusPrecedencePrograms()...)
	programs = append(programs, indexedStringPrograms()...)
	base := t.TempDir()
	piDirs, pigDirs := make([]string, len(programs)), make([]string, len(programs))
	for i := range programs {
		piDirs[i], pigDirs[i] = filepath.Join(base, fmt.Sprintf("pi-%d", i)), filepath.Join(base, fmt.Sprintf("pig-%d", i))
		for _, dir := range []string{piDirs[i], pigDirs[i]} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	piWrite := runPiJSONL(t, "sqlite-write", programs, piDirs)
	pigWrite := runPigSQLite(t, programs, pigDirs, false)
	ops, failures, differing := map[string]int{}, 0, 0
	for i, program := range programs {
		differing += compareOracleResults(t, fmt.Sprintf("write program %d", i), program, pigWrite[i], piWrite[i], differing, ops, &failures)
	}
	// What each writer left, before any reopen: the same schema, and the same rows byte for byte.
	differing += compareSQLiteFiles(t, piDirs, pigDirs)
	piReadsPi := runPiJSONL(t, "sqlite-read", programs, piDirs)
	piReadsPig := runPiJSONL(t, "sqlite-read", programs, pigDirs)
	pigReadsPi := runPigSQLite(t, programs, piDirs, true)
	pigReadsPig := runPigSQLite(t, programs, pigDirs, true)
	for i, program := range programs {
		for label, got := range map[string][]oracleResult{"Pi reads Pig's file": piReadsPig[i], "Pig reads Pi's file": pigReadsPi[i], "Pig reads its own file": pigReadsPig[i]} {
			differing += compareOracleResults(t, fmt.Sprintf("%s, program %d", label, i), program, got, piReadsPi[i], differing, map[string]int{}, new(int))
		}
	}
	if differing > 8 {
		t.Errorf("%d answers differ", differing)
	}
	if failures < 20 || ops["commit"] < 300 || ops["scanEntries"] < 50 {
		t.Errorf("probes are one-sided: %v, %d rejections", ops, failures)
	}
}

type sqliteDump struct {
	Schema []map[string]any            `json:"schema"`
	Tables map[string][]map[string]any `json:"tables"`
}

// dumpSQLite reads each directory's store.db through node:sqlite, independent of either storage implementation.
func dumpSQLite(t *testing.T, dirs []string) []sqliteDump {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"dirs": dirs})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/memory_oracle.mjs", filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages"), "sqlite-dump")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("sqlite dump: %v\n%s", err, &stderr)
	}
	var dumps []sqliteDump
	if err := json.Unmarshal(output, &dumps); err != nil {
		t.Fatal(err)
	}
	if len(dumps) != len(dirs) {
		t.Fatalf("dumped %d of %d files", len(dumps), len(dirs))
	}
	return dumps
}

// compareSQLiteFiles compares the schema and every row of Pi's and Pig's files. Indexed text columns (task kind, request ID, document kind and key) must match byte for byte, because a lookup compares them as stored; so must every record column except tasks', submissions' and entries' with model, edits or byTaskId, whose member order the generator does not reproduce (those compare decoded).
func compareSQLiteFiles(t *testing.T, piDirs, pigDirs []string) int {
	t.Helper()
	piDumps, pigDumps := dumpSQLite(t, piDirs), dumpSQLite(t, pigDirs)
	differing := 0
	for i := range piDumps {
		pi, pig := piDumps[i], pigDumps[i]
		for _, table := range []string{"tasks", "submissions", "entries"} {
			for _, rows := range [][]map[string]any{pi.Tables[table], pig.Tables[table]} {
				for _, row := range rows {
					// An entry's model, data, edits and byTaskId come in the generator's order in Pi's row and in the record's field order in Pig's, as in compareJSONLBytes.
					if text := row["record"].(string); table == "entries" && !strings.Contains(text, `"model":`) && !strings.Contains(text, `"edits":`) && !strings.Contains(text, `"byTaskId":`) {
						continue
					}
					var decoded any
					if err := json.Unmarshal([]byte(row["record"].(string)), &decoded); err != nil {
						t.Fatalf("%s record %v: %v", table, row["record"], err)
					}
					row["record"] = decoded
				}
			}
		}
		if !reflect.DeepEqual(pi.Schema, pig.Schema) {
			if differing++; differing <= 4 {
				t.Errorf("program %d: schema differs\n  Pig %v\n  Pi  %v", i, pig.Schema, pi.Schema)
			}
		}
		for table, rows := range pi.Tables {
			if !reflect.DeepEqual(rows, pig.Tables[table]) {
				if differing++; differing <= 4 {
					t.Errorf("program %d: table %s differs\n  Pig %.1500v\n  Pi  %.1500v", i, table, pig.Tables[table], rows)
				}
			}
		}
		if len(pig.Tables) != len(pi.Tables) {
			differing++
			t.Errorf("program %d: Pig's file has %d tables, Pi's %d", i, len(pig.Tables), len(pi.Tables))
		}
	}
	return differing
}
