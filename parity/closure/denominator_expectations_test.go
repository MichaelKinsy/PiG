package closure

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Reviewed Pig-owned denominators that no generated ledger records.
const (
	reviewedPortMapRows      = 462 // PORT_MAP.md rows; every row carries a status and is provisional.
	reviewedFixedSourceFiles = 4   // families.toml, PORT_MAP.md, parity/coverage.md, DIVERGENCES.md.
)

type denominatorExpectation struct {
	items       int
	provisional int
}

// fileDenominators counts every ledger row and every reviewed (non-pending)
// status straight from the files on disk, independently of the importer, so
// an upstream leap changes the expectation only through the regenerated
// ledgers themselves.
func fileDenominators(t *testing.T, root string) (map[string]denominatorExpectation, int) {
	t.Helper()
	jsonSources, tomlSources := currentDenominatorSources()
	want := make(map[string]denominatorExpectation)
	for _, source := range jsonSources {
		var document map[string]json.RawMessage
		readDocument(t, root, source.path, func(data []byte) error { return json.Unmarshal(data, &document) })
		var expectation denominatorExpectation
		for _, list := range source.lists {
			var rows []map[string]any
			if raw, ok := document[list.key]; ok {
				if err := json.Unmarshal(raw, &rows); err != nil {
					t.Fatalf("%s %s: %v", source.path, list.key, err)
				}
			}
			expectation.items += len(rows)
			expectation.provisional += reviewedRows(rows, list.statusField)
		}
		want[source.dataset] = expectation
	}
	for _, source := range tomlSources {
		var document map[string]any
		readDocument(t, root, source.path, func(data []byte) error { return toml.Unmarshal(data, &document) })
		var rows []map[string]any
		if list, ok := document[source.listKey].([]map[string]any); ok {
			rows = list
		}
		want[source.dataset] = denominatorExpectation{items: len(rows), provisional: reviewedRows(rows, source.statusField)}
	}
	var families struct {
		Families map[string]any `toml:"families"`
	}
	readDocument(t, root, "parity/families.toml", func(data []byte) error { return toml.Unmarshal(data, &families) })
	want["family"] = denominatorExpectation{items: len(families.Families)}
	scenarios := 0
	if err := filepath.WalkDir(filepath.Join(root, "parity/scenarios"), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".toml" {
			scenarios++
		}
		return nil
	}); err != nil {
		t.Fatalf("count scenarios: %v", err)
	}
	want["scenario"] = denominatorExpectation{items: scenarios}
	want["port-map"] = markdownRows(t, root, "PORT_MAP.md")
	want["coverage-row"] = denominatorExpectation{items: markdownRows(t, root, "parity/coverage.md").items}
	divergences := activeDivergenceCount(t)
	want["divergence"] = denominatorExpectation{items: divergences, provisional: divergences}
	sourceFiles := len(jsonSources) + len(tomlSources) + scenarios + reviewedFixedSourceFiles
	return want, sourceFiles
}

func readDocument(t *testing.T, root, path string, decode func([]byte) error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(data); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// reviewedRows counts rows whose status is set and not pending, the rows the
// importer turns into provisional claims.
func reviewedRows(rows []map[string]any, statusField string) int {
	if statusField == "" {
		return 0
	}
	count := 0
	for _, row := range rows {
		status, _ := row[statusField].(string)
		if status != "" && status != "pending" && status != "⬜" {
			count++
		}
	}
	return count
}

// markdownRows counts the upstream-file rows of a PORT_MAP-shaped table and
// the rows whose status column is reviewed (not ⬜).
func markdownRows(t *testing.T, root, path string) denominatorExpectation {
	t.Helper()
	var expectation denominatorExpectation
	readDocument(t, root, path, func(data []byte) error {
		for line := range strings.SplitSeq(string(data), "\n") {
			if !strings.HasPrefix(line, "| `packages/") {
				continue
			}
			expectation.items++
			if fields := strings.Split(line, "|"); len(fields) > 3 && reviewedRows([]map[string]any{{"status": strings.TrimSpace(fields[3])}}, "status") == 1 {
				expectation.provisional++
			}
		}
		return nil
	})
	return expectation
}
