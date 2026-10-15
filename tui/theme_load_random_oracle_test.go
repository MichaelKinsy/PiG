package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type themeLoadResult struct {
	Error      string            `json:"error"`
	Name       string            `json:"name"`
	Appearance string            `json:"appearance"`
	Fg         map[string]string `json:"fg"`
	Bg         map[string]string `json:"bg"`
}

// TestLoadThemeFromPathOnRandomColorsMatchesPi: LoadThemeFromPath (theme.ts loadThemeFromPath, resolveVarRefs, withThemeColorFallbacks, Theme colour conversion) over the dark theme with random colour values: hex spellings, 256-palette indices, empty strings, oklch() and okhsl() strings, variable chains, cycles and references to missing variables; every foreground and background escape, the appearance and the error text in truecolor and 256-colour mode.
func TestLoadThemeFromPathOnRandomColorsMatchesPi(t *testing.T) {
	base, err := os.ReadFile("../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/theme/dark.json")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20261012))
	good := false
	hex := func() string {
		digits := "0123456789abcdefABCDEF"
		n := []int{3, 6, 6, 6, 4, 8, 5, 7}[rng.Intn(8)]
		if good {
			n = []int{3, 6}[rng.Intn(2)]
		}
		b := []byte{'#'}
		for range n {
			b = append(b, digits[rng.Intn(len(digits))])
		}
		return string(b)
	}
	ok := func() string {
		n := 6
		if good {
			n = 4
		}
		switch rng.Intn(n) {
		case 0:
			return fmt.Sprintf("oklch(%.2f %.3f %d)", rng.Float64(), rng.Float64()*0.4, rng.Intn(360))
		case 1:
			return fmt.Sprintf("OKLCH(%d%% %.2f %ddeg)", rng.Intn(101), rng.Float64()*0.4, rng.Intn(720)-180)
		case 2:
			return fmt.Sprintf("okhsl(%d %.2f %.2f)", rng.Intn(360), rng.Float64(), rng.Float64())
		case 3:
			return fmt.Sprintf("okhsl(%d, %d%%, %d%%)", rng.Intn(360), rng.Intn(101), rng.Intn(101))
		case 4:
			return "oklch(2 1 400)"
		default:
			return "okhsl(x y z)"
		}
	}
	vars := []string{"va", "vb", "vc", "accent"}
	value := func(allowRef bool) any {
		n := 10
		if good {
			n = 8
		}
		switch rng.Intn(n) {
		case 0, 1, 2:
			return hex()
		case 3:
			return rng.Intn(256)
		case 4:
			return ""
		case 5, 6:
			return ok()
		case 7:
			if allowRef {
				return vars[rng.Intn(len(vars))]
			}
			return hex()
		case 8:
			return []int{256, -1, 1000}[rng.Intn(3)]
		default:
			return "missingvar"
		}
	}
	dir := t.TempDir()
	const documents = 400
	for i := range documents {
		var doc map[string]any
		if err := json.Unmarshal(base, &doc); err != nil {
			t.Fatal(err)
		}
		colors := doc["colors"].(map[string]any)
		keys := make([]string, 0, len(colors))
		for k := range colors {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		good = rng.Intn(10) < 7
		v := doc["vars"].(map[string]any)
		for _, name := range vars {
			v[name] = value(false)
		}
		if good {
			v["va"], v["vb"] = "vb", "vc"
		} else {
			for _, name := range vars {
				v[name] = value(true)
			}
		}
		doc["vars"] = v
		for range 1 + rng.Intn(12) {
			colors[keys[rng.Intn(len(keys))]] = value(true)
		}
		if rng.Intn(4) == 0 {
			for _, k := range []string{"scrollbarTrack", "scrollbarThumb", "thinkingMax", "searchMatchBg", "searchMatchText"} {
				if rng.Intn(2) == 0 {
					delete(colors, k)
				}
			}
		}
		if rng.Intn(5) == 0 {
			doc["appearance"] = []string{"dark", "light"}[rng.Intn(2)]
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", i)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/theme_load.mjs", pigversion.UpstreamPackageVersion, dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []map[string]themeLoadResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != documents {
		t.Fatalf("oracle answered %d documents", len(expected))
	}
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	failures, loaded := 0, 0
	for i, entry := range expected {
		for _, mode := range []string{"truecolor", "256color"} {
			want := entry[mode]
			SetCapabilities(TerminalCapabilities{TrueColor: mode == "truecolor"})
			theme, err := LoadThemeFromPath(filepath.Join(dir, fmt.Sprintf("%d.json", i)), TerminalColorMode(mode))
			got := themeLoadResult{}
			if err != nil {
				got.Error = err.Error()
			} else {
				got.Name, got.Appearance = theme.Name, string(theme.Appearance())
				got.Fg, got.Bg = theme.ANSIPalette()
				loaded++
			}
			if want.Error == "" && len(want.Fg) == 0 {
				t.Fatalf("doc %d %s: oracle gave neither error nor palette", i, mode)
			}
			if got.Error != want.Error || got.Name != want.Name || got.Appearance != want.Appearance || !mapsEqualJSON(got.Fg, want.Fg) || !mapsEqualJSON(got.Bg, want.Bg) {
				if failures++; failures <= 6 {
					t.Errorf("doc %d %s:\n Pig %s\n Pi  %s", i, mode, summarizeThemeLoad(got), summarizeThemeLoad(want))
				}
			}
		}
	}
	if loaded < documents/2 {
		t.Fatalf("only %d of %d loads succeeded in Pig; the corpus no longer exercises resolved colours", loaded, 2*documents)
	}
	if failures > 0 {
		t.Fatalf("%d of %d loads differ from Pi", failures, 2*documents)
	}
}

func mapsEqualJSON(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func summarizeThemeLoad(r themeLoadResult) string {
	if r.Error != "" {
		return "error " + r.Error
	}
	return fmt.Sprintf("%s/%s fg=%d bg=%d", r.Name, r.Appearance, len(r.Fg), len(r.Bg))
}
