package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestShortTempBase(t *testing.T) {
	const localAppData = `C:\Users\runner\AppData\Local`
	longTemp := `D:\a\_temp\` + strings.Repeat("d", maxShortTempDirBytes)
	for _, test := range []struct {
		name, goos, temp, localAppData, want string
	}{
		{"unix uses /tmp whatever TMPDIR is", "darwin", "/var/folders/zz/" + strings.Repeat("d", 80), localAppData, "/tmp"},
		{"windows keeps a short temp directory", "windows", `C:\T`, localAppData, `C:\T`},
		{"windows keeps a temp directory that just fits", "windows", `D:\` + strings.Repeat("d", maxShortTempDirBytes-len(`D:\`)-len(`\pe`)-10), localAppData, `D:\` + strings.Repeat("d", maxShortTempDirBytes-len(`D:\`)-len(`\pe`)-10)},
		{"windows falls back when the temp directory is one byte too long", "windows", `D:\` + strings.Repeat("d", maxShortTempDirBytes-len(`D:\`)-len(`\pe`)-10+1), localAppData, localAppData + `\pig\s`},
		{"windows falls back from a long temp directory", "windows", longTemp, localAppData, localAppData + `\pig\s`},
		{"windows without local app data keeps the long temp directory", "windows", longTemp, "", longTemp},
	} {
		if got := shortTempBase(test.goos, test.temp, test.localAppData, "pe"); strings.ReplaceAll(got, "/", `\`) != strings.ReplaceAll(test.want, "/", `\`) {
			t.Errorf("%s: shortTempBase = %q, want %q", test.name, got, test.want)
		}
	}
}

// The directory ShortTempDir makes on Windows is the temp directory, the prefix and the ten digits os.MkdirTemp adds; it is kept when that is at most maxShortTempDirBytes (70) and the base falls back to %LOCALAPPDATA%\pig\s from 71. The longest socket path below it is \pig\h<uint32>\<count>.s, 26 bytes (28 before the shorter leaf), so a directory of 70 bytes holds a path of 96 against the 107 a sun_path allows, and the bound stays where it was: a directory of 69 or 70 bytes takes the same branch as before.
func TestShortTempBaseAtTheEdgeOfTheBound(t *testing.T) {
	const localAppData = `C:\Users\runner\AppData\Local`
	const worstSocketPathBelow = len(`\pig\h4294967295\1z141z3.s`)
	if maxShortTempDirBytes != 70 || worstSocketPathBelow != 26 {
		t.Fatalf("the bound is %d and the longest socket path below a directory %d bytes, the rows name 70 and 26", maxShortTempDirBytes, worstSocketPathBelow)
	}
	for _, test := range []struct {
		dirBytes int
		kept     bool
	}{{68, true}, {69, true}, {70, true}, {71, false}} {
		temp := `D:\` + strings.Repeat("d", test.dirBytes-10-len(`D:\`)-len(`\pe`))
		if got := len(temp) + len(`\pe`) + 10; got != test.dirBytes {
			t.Fatalf("a temp directory of %d bytes makes a ShortTempDir of %d bytes, want %d", len(temp), got, test.dirBytes)
		}
		want := localAppData + `\pig\s`
		if test.kept {
			want = temp
			if limit := 107; test.dirBytes+worstSocketPathBelow > limit {
				t.Errorf("a kept directory of %d bytes holds a socket path of %d bytes, over the %d a sun_path allows", test.dirBytes, test.dirBytes+worstSocketPathBelow, limit)
			}
		}
		if got := shortTempBase("windows", temp, localAppData, "pe"); strings.ReplaceAll(got, "/", `\`) != want {
			t.Errorf("a ShortTempDir of %d bytes: shortTempBase = %q, want %q", test.dirBytes, got, want)
		}
	}
}

// A package whose TestMain makes its root with MkdirShortTemp has a fixture depth that does not follow TMPDIR: the root is directly in /tmp.
func TestMkdirShortTempIgnoresTMPDIR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the host temp directory")
	}
	t.Setenv("PIG_TEST_TEMP_ROOT", "")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "a", "b", "c", "d"))
	root, err := MkdirShortTemp("pig-depth-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if filepath.Dir(root) != "/tmp" || !strings.HasPrefix(filepath.Base(root), "pig-depth-") {
		t.Errorf("MkdirShortTemp = %q, want a directory named pig-depth-* directly in /tmp", root)
	}
}

// Setting PIG_TEST_TEMP_ROOT moves MkdirShortTemp (and ShortTempDir) under that directory without resolving symbolic links in it, so a gate job on Linux can exercise the macOS /tmp -> /private/tmp path shape. Unset keeps /tmp.
func TestMkdirShortTempRespectsTempRootSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PIG_TEST_TEMP_ROOT overrides the non-Windows /tmp base")
	}
	realDir := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(t.TempDir(), "link")
	RequireSymlink(t, realDir, linkDir)

	t.Setenv("PIG_TEST_TEMP_ROOT", linkDir)
	got, err := MkdirShortTemp("pig-link-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(got) })
	if filepath.Dir(got) != linkDir || !strings.HasPrefix(filepath.Base(got), "pig-link-") {
		t.Errorf("MkdirShortTemp with PIG_TEST_TEMP_ROOT=%q = %q, want a directory named pig-link-* directly in %q (unresolved)", linkDir, got, linkDir)
	}
	short := ShortTempDir(t, "pig-short-")
	if filepath.Dir(short) != linkDir || !strings.HasPrefix(filepath.Base(short), "pig-short-") {
		t.Errorf("ShortTempDir with PIG_TEST_TEMP_ROOT=%q = %q, want a directory named pig-short-* directly in %q (unresolved)", linkDir, short, linkDir)
	}

	t.Setenv("PIG_TEST_TEMP_ROOT", "")
	unset, err := MkdirShortTemp("pig-unset-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(unset) })
	if filepath.Dir(unset) != "/tmp" || !strings.HasPrefix(filepath.Base(unset), "pig-unset-") {
		t.Errorf("MkdirShortTemp with unset PIG_TEST_TEMP_ROOT = %q, want a directory named pig-unset-* directly in /tmp", unset)
	}
}

// Every Windows-built caller of ShortTempDir or MkdirShortTemp must use a prefix short enough that the fallback directory on GitHub's Windows runner
// (C:\Users\runneradmin\AppData\Local\pig\s\<prefix><10 digits>) stays within maxShortTempDirBytes (70). Checking literal prefixes on Linux catches
// overlong prefixes before they reach a Windows runner.
func TestWindowsShortTempPrefixesFitRunnerBudget(t *testing.T) {
	const runnerFallbackDir = `C:\Users\runneradmin\AppData\Local\pig\s`
	const uint32Digits = 10
	maxPrefix := maxShortTempDirBytes - len(runnerFallbackDir) - 1 - uint32Digits
	root, files := windowsTestFiles(t)
	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var fn string
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				fn = fun.Sel.Name
			case *ast.Ident:
				fn = fun.Name
			}
			var argIdx int
			switch fn {
			case "ShortTempDir":
				argIdx = 1
			case "MkdirShortTemp":
				argIdx = 0
			default:
				return true
			}
			if len(call.Args) <= argIdx {
				return true
			}
			lit, ok := call.Args[argIdx].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			prefix, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			checked++
			if len(prefix) > maxPrefix {
				pos := fset.Position(call.Pos())
				dirLen := len(runnerFallbackDir) + 1 + len(prefix) + uint32Digits
				t.Errorf("%s:%d: %s prefix %q (%d bytes) makes a %d-byte directory under %s, over maxShortTempDirBytes (%d; max prefix %d bytes)",
					filepath.ToSlash(rel), pos.Line, fn, prefix, len(prefix), dirLen, runnerFallbackDir, maxShortTempDirBytes, maxPrefix)
			}
			return true
		})
	}
	if checked < 10 {
		t.Fatalf("checked only %d ShortTempDir/MkdirShortTemp callers; expected at least 10", checked)
	}
}

func TestEvalSymlinksResolvesDirectoryLink(t *testing.T) {
	base := EvalSymlinks(t, t.TempDir())
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	RequireDirectoryLink(t, target, link)
	if got := EvalSymlinks(t, link); got != target {
		t.Fatalf("EvalSymlinks(%q) = %q, want %q", link, got, target)
	}
}
