package widthx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piUtilsSource is the pinned Pi's utils.ts with the commits PiG follows ahead of the pin applied, so the oracles that run it compare
// PiG with the Pi it follows. Each replacement must match exactly once: when the pin reaches the commit, the anchor stops matching and
// the overlay goes with its D record.
func piUtilsSource(t testing.TB, dir string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(dir, "utils.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, overlay := range []struct{ d, from, to string }{{
		// D300, Pi main 25490ea49: an APC code (a cursor marker) before the range is not carried into the slice.
		"D300",
		"} else if (currentCol < startCol) pendingAnsi += ansi.code;\n\t\t\ti += ansi.length;",
		"} else if (currentCol < startCol && !ansi.code.startsWith(\"\\x1b_\")) pendingAnsi += ansi.code;\n\t\t\ti += ansi.length;",
	}} {
		if strings.Count(text, overlay.from) != 1 {
			t.Fatalf("overlay %s no longer matches the pinned utils.ts exactly once: remove it with its D record", overlay.d)
		}
		text = strings.Replace(text, overlay.from, overlay.to, 1)
	}
	return []byte(text)
}
