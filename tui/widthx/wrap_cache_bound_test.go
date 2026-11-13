package widthx

import (
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// longText is n lines that each open an SGR state the next one closes, so the state crosses line boundaries.
func longText(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "\x1b[38;5;" + strconv.Itoa(1+i%200) + "mline " + strconv.Itoa(i) + " export function step(input) { return input.length; }"
		if i%3 == 0 {
			lines[i] += "\x1b[39m"
		}
	}
	return lines
}

// retainedBytes is the live heap that fn's result holds, which fn must return and which is kept alive until the second reading.
func retainedBytes[T any](fn func() T) (T, uint64) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	v := fn()
	runtime.GC()
	runtime.ReadMemStats(&after)
	return v, after.HeapAlloc - before.HeapAlloc
}

// A cache holds the rows of a bounded prefix of its text however long the text is: the memo of a 60,000 line text is not as large
// as its own rows.
func TestLineWrapCacheRetainsABoundedMemo(t *testing.T) {
	lines := longText(60000)
	text := strings.Join(lines, "\n")
	const width = 100
	pad := func(row string) string { return row + strings.Repeat(" ", max(0, width-len(row))) }
	var rowsHeld int
	cache, held := retainedBytes(func() *LineWrapCache {
		c := &LineWrapCache{}
		c.Wrap(text, width, pad)
		c.Wrap(text+"\ntail", width, pad)
		return c
	})
	for _, l := range cache.lines {
		rowsHeld += len(l.rows)
	}
	runtime.KeepAlive(cache)
	if rowsHeld > maxCachedRows {
		t.Errorf("the cache holds %d rows, the bound is %d", rowsHeld, maxCachedRows)
	}
	// The result of the wraps is garbage here: what is live is the cache, whose bound is its rows (about width bytes and a
	// string header each) and line entries. The rows of 60,000 lines would be above 7 MB.
	if limit := uint64(maxCachedRows) * (width + 400); held > limit {
		t.Errorf("a cache of a 60,000 line text retains %d bytes, more than %d", held, limit)
	}
}

// Past the bound a changed text still wraps as WrapTextWithAnsi does: lines inside the memo are served, the lines after it are
// wrapped again, whichever line changes and whichever state crosses the bound.
func TestLineWrapCacheAcrossItsBoundMatchesWrapTextWithAnsi(t *testing.T) {
	lines := longText(maxCachedRows + 900)
	const width = 40
	var cache LineWrapCache
	check := func(name string, ls []string) {
		t.Helper()
		text := strings.Join(ls, "\n")
		got, want := cache.Wrap(text, width, finishRow), wantWrapped(text, width, finishRow)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: the cache gives other rows than WrapTextWithAnsi", name)
		}
		rows := 0
		for _, l := range cache.lines {
			rows += len(l.rows)
		}
		if rows > maxCachedRows {
			t.Fatalf("%s: the cache holds %d rows", name, rows)
		}
	}
	check("first", lines)
	check("same", lines)
	edit := func(at int, to string) []string {
		out := slices.Clone(lines)
		out[at] = to
		return out
	}
	check("edit inside", edit(100, "changed \x1b[1mline"))
	check("edit inside, state closed", edit(101, "\x1b[0m plain"))
	check("edit near the bound", edit(maxCachedRows/3, "near \x1b[4m the bound"))
	check("edit past the bound", edit(maxCachedRows+500, "past \x1b[31m the bound"))
	check("grow", append(slices.Clone(lines), "one more", "and another"))
	check("shrink under the bound", lines[:50])
	check("grow again", lines)
	check("shorter past the bound", lines[:maxCachedRows+10])
}

// The lines inside the bound are still served from the memo when the text goes on past it.
func TestLineWrapCacheServesTheLinesInsideItsBound(t *testing.T) {
	lines := longText(maxCachedRows + 2000)
	const width = 80
	text := strings.Join(lines[:200], "\n")
	var cache LineWrapCache
	cache.Wrap(text, width, finishRow)
	n := 0
	changed := testing.AllocsPerRun(20, func() {
		n++
		cache.Wrap(text+"\ntail "+strings.Repeat("x", n%40), width, finishRow)
	})
	whole := testing.AllocsPerRun(20, func() {
		for _, row := range WrapTextWithAnsi(text+"\ntail 0", width) {
			_ = finishRow(row)
		}
	})
	if changed*10 > whole {
		t.Errorf("a change in the last line allocates %.0f, a whole wrap %.0f", changed, whole)
	}
}
