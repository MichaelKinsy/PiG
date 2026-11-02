package codingagent

// pi: packages/coding-agent/src/utils/windows-self-update.ts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type quarantineOracleCase struct {
	Name       string
	Files      map[string]string
	PackageDir string
	Loaded     []string
	Cleanup    bool
	// Moved is how many images Pi quarantines in the case; it keeps a case from passing because the oracle's loaded-image list never took effect.
	Moved int
}

// quarantineTree lists root's files and directories with the quarantine directory and its per-run directory under a node_modules renamed to Q and RUN: Pi names
// the quarantine .pi-native-quarantine, pig .pig-native-quarantine, and a run directory carries the time, the pid and a random UUID.
func quarantineTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for i, part := range parts {
			if lower := strings.ToLower(part); lower == ".pi-native-quarantine" || lower == ".pig-native-quarantine" {
				parts[i] = "Q"
				if i+1 < len(parts) {
					parts[i+1] = "RUN"
				}
				break
			}
		}
		entry := strings.Join(parts, "/")
		if d.IsDir() {
			out = append(out, entry+"/")
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, entry+" = "+string(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func writeQuarantineLayout(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// quarantineWindowsNativeDependencies and cleanupWindowsSelfUpdateQuarantine run by Pi's compiled windows-self-update.js, with process.report's loaded shared objects
// set to the case's list, against the same layout run by pig's quarantineNativeDependencies and CleanupWindowsSelfUpdateQuarantine: the files under the tree must match,
// that is which loaded images move to the quarantine of the nearest node_modules (any letter case) at their package-relative path and are copied back, which stay (an
// image outside the package, a sibling package sharing the directory-name prefix, a missing one, a second listing of one image), and what the cleanup removes.
func TestQuarantineMatchesPi(t *testing.T) {
	cases := []quarantineOracleCase{
		{
			Name: "loaded images move and return",
			Files: map[string]string{
				"node_modules/pkg/bin/pig.exe":     "running image",
				"node_modules/pkg/lib/native.node": "addon",
				"node_modules/pkg/lib/other.txt":   "not loaded",
				"node_modules/pkg-two/lib.dll":     "sibling package",
				"node_modules/other/lib.dll":       "other package",
			},
			PackageDir: "node_modules/pkg",
			Loaded: []string{
				"node_modules/pkg/bin/pig.exe", "node_modules/other/lib.dll", "node_modules/pkg-two/lib.dll", "node_modules/pkg/bin/missing.dll",
				"node_modules/pkg/lib/native.node", "node_modules/pkg/bin/pig.exe",
			},
			Moved: 2,
		},
		{
			Name: "cleanup removes the quarantine",
			Files: map[string]string{
				"node_modules/pkg/bin/pig.exe": "running image",
			},
			PackageDir: "node_modules/pkg",
			Loaded:     []string{"node_modules/pkg/bin/pig.exe"},
			Cleanup:    true,
			Moved:      1,
		},
		{
			Name: "a scoped package",
			Files: map[string]string{
				"node_modules/@scope/other/bin/tool.dll": "scoped image",
			},
			PackageDir: "node_modules/@scope/other",
			Loaded:     []string{"node_modules/@scope/other/bin/tool.dll"},
			Moved:      1,
		},
		{
			Name: "node_modules in capitals",
			Files: map[string]string{
				"NODE_MODULES/pkg/bin/pig.exe": "running image",
			},
			PackageDir: "NODE_MODULES/pkg",
			Loaded:     []string{"NODE_MODULES/pkg/bin/pig.exe"},
			Moved:      1,
		},
		{
			Name: "the nearest node_modules above a nested package directory",
			Files: map[string]string{
				"node_modules/pkg/node_modules/inner/bin/pig.exe": "inner image",
			},
			PackageDir: "node_modules/pkg/node_modules/inner",
			Loaded:     []string{"node_modules/pkg/node_modules/inner/bin/pig.exe"},
			Moved:      1,
		},
		{
			Name: "no node_modules above the package",
			Files: map[string]string{
				"pkg/pig.exe": "running image",
			},
			PackageDir: "pkg",
			Loaded:     []string{"pkg/pig.exe"},
		},
		{
			Name: "no loaded image inside the package",
			Files: map[string]string{
				"node_modules/pkg/bin/pig.exe": "running image",
				"node_modules/other/lib.dll":   "other package",
			},
			PackageDir: "node_modules/pkg",
			Loaded:     []string{"node_modules/other/lib.dll"},
		},
		{
			Name: "cleanup without a quarantine",
			Files: map[string]string{
				"node_modules/pkg/bin/pig.exe": "running image",
			},
			PackageDir: "node_modules/pkg",
			Cleanup:    true,
		},
	}

	type piCase struct {
		Root       string   `json:"root"`
		PackageDir string   `json:"packageDir"`
		Loaded     []string `json:"loaded"`
		Cleanup    bool     `json:"cleanup"`
	}
	piRoot, pigRoot := t.TempDir(), t.TempDir()
	input := make([]piCase, len(cases))
	for i, c := range cases {
		piDir := filepath.Join(piRoot, "case"+string(rune('a'+i)))
		writeQuarantineLayout(t, piDir, c.Files)
		writeQuarantineLayout(t, filepath.Join(pigRoot, "case"+string(rune('a'+i))), c.Files)
		loaded := make([]string, len(c.Loaded))
		for j, rel := range c.Loaded {
			loaded[j] = filepath.Join(piDir, filepath.FromSlash(rel))
		}
		input[i] = piCase{Root: piDir, PackageDir: filepath.Join(piDir, filepath.FromSlash(c.PackageDir)), Loaded: loaded, Cleanup: c.Cleanup}
	}
	var beforeCleanup [][]string
	pioracle.Run(t, `
const { quarantineWindowsNativeDependencies, cleanupWindowsSelfUpdateQuarantine } = await load("pi-coding-agent/utils/windows-self-update.js");
const { readdirSync } = await import("node:fs");
const { join, relative } = await import("node:path");
const list = (root) => {
  const out = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      out.push(relative(root, path).split("\\").join("/") + (entry.isDirectory() ? "/" : ""));
      if (entry.isDirectory()) walk(path);
    }
  };
  walk(root);
  return out;
};
const before = [];
for (const test of input) {
  process.report.getReport = () => ({ sharedObjects: test.loaded });
  quarantineWindowsNativeDependencies(test.packageDir);
  before.push(list(test.root));
  if (test.cleanup) cleanupWindowsSelfUpdateQuarantine(test.packageDir);
}
emit(before);`, input, &beforeCleanup)

	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			piDir := input[i].Root
			pigDir := filepath.Join(pigRoot, "case"+string(rune('a'+i)))
			loaded := make([]string, len(c.Loaded))
			for j, rel := range c.Loaded {
				loaded[j] = filepath.Join(pigDir, filepath.FromSlash(rel))
			}
			packageDir := filepath.Join(pigDir, filepath.FromSlash(c.PackageDir))
			if err := quarantineNativeDependencies(t.Context(), packageDir, loaded); err != nil {
				t.Fatal(err)
			}
			if c.Cleanup {
				CleanupWindowsSelfUpdateQuarantine(packageDir)
			}
			want := quarantineTree(t, piDir)
			got := quarantineTree(t, pigDir)
			if !slices.Equal(got, want) {
				t.Errorf("pig tree:\n%s\nPi tree:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			// Pi's quarantined images are counted before its cleanup, which removes them in the cases that run one.
			moved := 0
			for _, entry := range beforeCleanup[i] {
				parts := strings.Split(entry, "/")
				if k := slices.Index(parts, ".pi-native-quarantine"); k >= 0 && len(parts) > k+2 && parts[len(parts)-1] != "" {
					moved++
				}
			}
			if c.Cleanup && slices.ContainsFunc(want, func(entry string) bool { return strings.Contains(entry, "/Q/") }) {
				t.Errorf("Pi's cleanup left a quarantine: %v", want)
			}
			if moved != c.Moved {
				t.Errorf("Pi quarantined %d images, want %d: %v", moved, c.Moved, want)
			}
		})
	}
}
