package tools

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// path-utils.ts resolveReadPath tries the path as resolved, then macOS spellings of it: a narrow no-break space before AM/PM, the NFD form, curly apostrophes, and NFD with curly apostrophes. The directory holds files under those
// spellings (plain, AM/PM, NFD, curly, combined) and each probe is a user's spelling of one of them; Pi's resolveReadPath and PiG's must return the same file.
func TestResolveReadPathMatchesPi(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		// d\u2019\u00e9j\u00e0.txt is precomposed with a curly apostrophe: only the curly step (not NFD plus curly) finds it from a straight quote.
		"d\u2019\u00e9j\u00e0.txt", "plain.txt", "Screenshot 2024 at 1.34\u202fPM.png", "Screenshot 2024 at 1.34 PM.png", "Capture d\u2019e\u0301cran 2024.png", "caf\u00e9.txt", "cafe\u0301.txt", "it\u2019s.txt", "it's.txt", "l\u2019e\u0301te\u0301.md",
		"Shot 9.01\u202fam.png", "\u00e9.txt", "\u00c5\u00f6.txt", "a b.txt", "x\u2019y\u2019.txt", "Zoe\u0308 \u2019.txt", "dir/in\u2019side.txt", "re\u0301sume\u0301.txt", "don\u2019t.txt", "Cre\u0300me \u2019s.txt", "dir/ins\u00efde.txt", "\ud55c.txt", "\u1112\u1161\u11ab.txt",
	}
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spellings := []string{
		"d'\u00e9j\u00e0.txt", "plain.txt", "@plain.txt", "r\u00e9sum\u00e9.txt", "re\u0301sume\u0301.txt", "don't.txt", "don\u2019t.txt", "Cr\u00e8me 's.txt", "Cr\u00e8me \u2019s.txt", "Screenshot 2024 at 1.34 PM.png", "Screenshot 2024 at 1.34 pm.png", "Screenshot 2024 at 1.34\u202fPM.png", "Screenshot 2024 at 1.34\u00a0PM.png", "Shot 9.01 AM.png", "Shot 9.01 am.png",
		"Capture d'\u00e9cran 2024.png", "Capture d'e\u0301cran 2024.png", "Capture d\u2019\u00e9cran 2024.png", "caf\u00e9.txt", "cafe\u0301.txt", "its.txt", "it's.txt", "it\u2019s.txt", "l'\u00e9t\u00e9.md", "l'e\u0301te\u0301.md",
		"\u00e9.txt", "e\u0301.txt", "\u00c5\u00f6.txt", "A\u030ao\u0308.txt", "a\u00a0b.txt", "a\u2003b.txt", "a\u202fb.txt", "x'y'.txt", "x'y\u2019.txt", "Zo\u00eb '.txt", "dir/in's\u00efde.txt", "dir/in'side.txt", "dir/ins\u00efde.txt", "dir/ins\u00efde.txt ",
		"\ud55c.txt", "\u1112\u1161\u11ab.txt", "\u1112\u1161\u11ab.txt", "./plain.txt", "dir/../plain.txt", "missing.txt", "", "@", "~/x", "file://" + filepath.ToSlash(dir) + "/plain.txt", dir + "/caf\u00e9.txt", dir + "/it's.txt",
		"Screenshot 2024 at 1.34 PM.PNG", "x AM. y.txt", "' PM.txt",
	}
	r := rand.New(rand.NewPCG(3, 9))
	var probes []string
	probes = append(probes, spellings...)
	for range 300 {
		a := spellings[r.IntN(len(spellings))]
		b := spellings[r.IntN(len(spellings))]
		switch r.IntN(3) {
		case 0:
			probes = append(probes, strings.ReplaceAll(a, " ", "\u202f"))
		case 1:
			probes = append(probes, strings.ReplaceAll(a, "\u2019", "'"))
		default:
			ra, rb := []rune(a), []rune(b)
			probes = append(probes, string(ra[:len(ra)/2])+string(rb[len(rb)/2:]))
		}
	}
	var want []string
	pioracle.Run(t, `
const { resolveReadPath } = await load("pi-coding-agent/core/tools/path-utils.js");
emit(input.paths.map((p) => resolveReadPath(p, input.cwd)));
`, struct {
		Paths []string `json:"paths"`
		CWD   string   `json:"cwd"`
	}{probes, dir}, &want, "HOME=/home/oracle")
	t.Setenv("HOME", "/home/oracle")
	bad := 0
	for i, p := range probes {
		got, err := ResolveReadPath(p, dir)
		if err != nil {
			got = "error: " + err.Error()
		}
		if got != want[i] {
			if bad++; bad <= 8 {
				t.Errorf("ResolveReadPath(%q): PiG %q, Pi %q", p, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d differ", bad, len(probes))
	}
}
