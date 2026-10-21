package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

// vectorMarker names the directory of vectors generated from Pi's own
// implementation at the pin. A test file that reads from it and calls a
// helper proves the helper against Pi, not just by name; the ledger counts a
// realized cell as reached when such a file references it.
const vectorMarker = "test/parity/sdk-vectors/"

// vectorTestDirs are where each SDK keeps its own unit tests.
var vectorTestDirs = map[string]struct {
	dir  string
	test func(path string) bool
}{
	"go":     {"extensions/sdk", func(p string) bool { return strings.HasSuffix(p, "_test.go") }},
	"rust":   {"extensions/sdk-rs", func(p string) bool { return strings.HasSuffix(p, ".rs") && strings.Contains(p, "test") }},
	"python": {"extensions/sdk-py/tests", func(p string) bool { return strings.HasSuffix(p, ".py") }},
}

// loadVectorCorpus concatenates the SDK's test files that read Pi-generated
// vectors.
func loadVectorCorpus(root, sdk string) (string, error) {
	spec := vectorTestDirs[sdk]
	var b strings.Builder
	err := filepath.WalkDir(filepath.Join(root, spec.dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "target", "__pycache__", "testdata", ".venv":
				return filepath.SkipDir
			}
			return nil
		}
		if !spec.test(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(vectorMarker)) {
			b.Write(data)
			b.WriteByte('\n')
		}
		return nil
	})
	return b.String(), err
}

// typeOnlyCounts counts, per ledger module, the declarations with no runtime
// value: they are identity by kind and take no ledger row.
func typeOnlyCounts(root string, rows []ledgerRow) (map[string]int, error) {
	raw, err := os.ReadFile(filepath.Join(root, "test/parity/interfaces/upstream-v"+coding.UpstreamVersion+".json"))
	if err != nil {
		return nil, err
	}
	var inv inventoryFile
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, err
	}
	listed := map[string]bool{}
	for _, r := range rows {
		listed[r.Key] = true
	}
	out := map[string]int{}
	for _, p := range piPackages {
		for _, i := range inv.Interfaces {
			if i.Package != p.name || i.Entrypoint != p.entrypoint {
				continue
			}
			switch i.Kind {
			case "interface", "type-alias":
				out[p.module]++
			case "external":
				if !listed[p.module+": "+i.Name] {
					out[p.module]++
				}
			}
		}
	}
	return out, nil
}

// loadLedgerInput gathers the generated denominator, the SDK symbols and the
// conformance corpora the ledger checks read.
func loadLedgerInput(root string, m *matrix, corpora map[string]string, regen bool) (ledgerInput, error) {
	in := ledgerInput{Rows: ledgerRows(m)}
	var err error
	if in.File, err = loadLedger(root); err != nil {
		if !regen || !errors.Is(err, fs.ErrNotExist) {
			return in, err
		}
		in.File = defaultLedger()
	}
	if in.Usage, err = loadUsage(root); err != nil {
		return in, err
	}
	if in.Divergences, err = loadDivergenceIDs(root); err != nil {
		return in, err
	}
	in.Symbols = map[string]symbolSet{}
	for _, sdk := range nativeSDKs {
		in.Symbols[sdk] = m.syms.bySDK[sdk]
	}
	in.Corpus = map[string]string{}
	for _, sdk := range nativeSDKs {
		vectors, err := loadVectorCorpus(root, sdk)
		if err != nil {
			return in, fmt.Errorf("%s vector tests: %w", sdk, err)
		}
		in.Corpus[sdk] = corpora[sdk] + "\n" + vectors
	}
	return in, nil
}

// regenerateLedger returns the ledger file with a row for every export of the
// denominator, in the generator's order, keeping every cell already set.
func regenerateLedger(in ledgerInput) []byte {
	return renderLedger(in.File, in.Rows)
}

// defaultLedger is the file a first regeneration starts from.
func defaultLedger() ledgerFile {
	return ledgerFile{Policy: ledgerPolicy{ReleaseTrain: "0.5.2", Trains: []string{"0.5.2"}, MaxGapTrains: 1, HighUse: 5}}
}
