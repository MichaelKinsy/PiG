package host

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// GO-ONLY (Pi's test/host/tiling.test.ts has no case for it; the rule is `tiling.ts` line 208, `#compare(page.after, known) >=
// 0`, ported in host_tiling.go `patch`): a page that begins exactly at the last row a cut run knows lies behind everything the
// run says, so it is loaded again; it is not patched. `>= 0` is the rule that holds when the bound and the cut run's last
// row are the same position; with `> 0` the page is patched like one the cut falls into: its rows are those the retired
// page's interval took (none: the run's rows all lie at or before the bound), `more` is set, and `retile` then indexes
// `rows[len(rows)-1]` of an open page.
//
// Why the case calls `patch` itself: the bound and the run's last row are one position only when an adoption of the group made
// a bound inside the interval of a page whose cut run a later commit of the group delivers, and the page behind the bound
// has run by then and is open. A bounded retired page's cut run ends strictly before its own end; the pages a cut makes behind
// its last row are loaded, not run, when the next adoption comes; an open page begins at a bound that was there before the
// group. The group of two commits that makes it was not found (see the state file), so the retired page's interval and its
// cut run are given by the test: the rows are the pages' real rows, the pages the real ones of a list a screen holds.
// pi: packages/chord/src/host/tiling.ts
func TestTilingAPageThatBeginsAtACutRunsLastRowIsLoadedAgain(t *testing.T) {
	w := tlHosted(t, 2, 5)
	w.more()
	w.more()
	// Pages [s5 s4] (nil, s4], [s3 s2] (s4, s2], [s1] (s2, open).
	tlEqual(t, "ids", w.ids(), []string{"s5", "s4", "s3", "s2", "s1"})
	lists := slices.Compact(listsOf(w.host)) // the attempt's phase and exposed slots hold the same list
	if len(lists) != 1 {
		t.Fatalf("lists = %d, want the screen's one", len(lists))
	}
	l := lists[0]
	l.mu.Lock()
	tiling := l.current
	l.mu.Unlock()

	tiling.mu.Lock()
	pages := append([]*tilingPage(nil), tiling.pages...)
	if len(pages) != 3 || pages[2].through != nil || !pages[2].ran {
		tiling.mu.Unlock()
		t.Fatalf("pages = %s, want three, the last open and run", l.describe())
	}
	// A retired page of (s4, open) whose run was cut at two rows: [s3 s2], more behind. Its last row is the open page's bound.
	retired := &tilingPage{after: pages[0].through}
	cutRows := append([]store.StoredRead(nil), pages[1].rows...)
	known := tiling.position(cutRows[len(cutRows)-1])
	if !reflect.DeepEqual(known, pages[2].after) {
		tiling.mu.Unlock()
		t.Fatalf("the cut run's last row = %v, the open page begins at %v: they must be one position", known, pages[2].after)
	}
	tiling.patch(retired, cutRows, true)
	after := append([]*tilingPage(nil), tiling.pages...)
	var (
		midSame      = len(after) == 3 && after[1] == pages[1]
		midRan       = len(after) == 3 && after[1].ran
		midMore      = len(after) == 3 && after[1].more
		midRows      = 0
		openSame     = len(after) == 3 && after[2] == pages[2]
		openRan      = len(after) == 3 && after[2].ran
		openMore     = len(after) == 3 && after[2].more
		openRows     = 0
		openAfter    store.Position
		openThru     store.Position
		oldOpenAfter = pages[2].after
		oldOpenRan   = pages[2].ran
		oldOpenRows  = len(pages[2].rows)
		oldOpenMore  = pages[2].more
	)
	if len(after) == 3 {
		midRows = len(after[1].rows)
		openRows = len(after[2].rows)
		openAfter = after[2].after
		openThru = after[2].through
	}
	tiling.mu.Unlock()

	if len(after) != 3 {
		t.Fatalf("pages after the patch = %d, want 3", len(after))
	}
	if !midSame || !midRan || midMore || midRows != 2 {
		t.Fatalf("the page the cut run ends in (s4, s2] (same=%v ran=%v more=%v rows=%d), want it as it was: its rows are all inside the cut run's interval", midSame, midRan, midMore, midRows)
	}
	if openSame || openRan || openRows != 0 || openMore {
		t.Fatalf("the open page that begins at the cut run's last row was patched in place (ran %v, rows %d, more %v), want a new page that has not run",
			oldOpenRan, oldOpenRows, oldOpenMore)
	}
	if !reflect.DeepEqual(openAfter, oldOpenAfter) || openThru != nil {
		t.Fatalf("the page loaded again = (%v, %v), want the open page's own interval (%v, open)", openAfter, openThru, oldOpenAfter)
	}
	// The page loads its rows itself: the list shows all five again, and the page is a live read (a touch moves its row).
	lsSettled(w.host)
	tlEqual(t, "ids", w.ids(), []string{"s5", "s4", "s3", "s2", "s1"})
	w.sessionsWrite(jsonObject("touch", []any{jsonObject("id", "s1", "touched", 50.0)}))
	tlEqual(t, "ids", w.ids(), []string{"s1", "s5", "s4", "s3", "s2"})
	w.close()
}

// tgPages is the screen's list in pages [s5 s4] (nil, s4], [s3 s2] (s4, s2], [s1] (s2, open): its tiling, and a copy of its pages.
func tgPages(t *testing.T) (w *tlWorld, tiling *tiling, pages []*tilingPage) {
	t.Helper()
	w = tlHosted(t, 2, 5)
	w.more()
	w.more()
	tlEqual(t, "ids", w.ids(), []string{"s5", "s4", "s3", "s2", "s1"})
	lists := slices.Compact(listsOf(w.host))
	if len(lists) != 1 {
		t.Fatalf("lists = %d, want the screen's one", len(lists))
	}
	lists[0].mu.Lock()
	tiling = lists[0].current
	lists[0].mu.Unlock()
	tiling.mu.Lock()
	pages = slices.Clone(tiling.pages)
	tiling.mu.Unlock()
	if len(pages) != 3 {
		t.Fatalf("pages = %s, want three", lists[0].describe())
	}
	return w, tiling, pages
}

// GO-ONLY (tiling.ts `#overlap`, host_tiling.go `overlap`): intervals are half-open, `(after, through]`, so two pages that meet at
// a bound share no position. Pi has no case that tells (its pages are rows no run shares a bound with); the rule matters to
// `patch`: a retired page's cut run must touch the pages it covers and not the page behind it, which a run that reads "through
// == after" as an overlap would load again. The mutant `<= 0` -> `< 0` in `before` is killed by both parts.
func TestTilingPagesThatMeetAtABoundDoNotOverlap(t *testing.T) {
	w, tiling, pages := tgPages(t)
	tiling.mu.Lock()
	for index := 0; index+1 < len(pages); index++ {
		if tiling.overlap(pages[index], pages[index+1]) || tiling.overlap(pages[index+1], pages[index]) {
			tiling.mu.Unlock()
			t.Fatalf("pages %d and %d meet at a bound and overlap", index, index+1)
		}
	}
	// A retired page of (s4, s2], the interval of the second page, whose run was cut at [s3]: the page behind it, (s2, open),
	// begins behind what the cut knows, and must stay as it is: it is not covered by the retired interval.
	retired := &tilingPage{after: pages[0].through, through: pages[1].through}
	cutRows := slices.Clone(pages[1].rows[:1])
	tiling.patch(retired, cutRows, true)
	last, first := tiling.pages[len(tiling.pages)-1], tiling.pages[0]
	again := last != pages[2] || !last.ran || len(last.rows) != 1
	tiling.mu.Unlock()
	if first != pages[0] {
		t.Fatal("the page before the retired interval was replaced")
	}
	if again {
		t.Fatalf("the page behind the retired interval was loaded again (it is %+v), want it as it was: a bound is not an overlap", last)
	}
	lsSettled(w.host)
	tlEqual(t, "ids", w.ids(), []string{"s5", "s4", "s3", "s2", "s1"})
	w.close()
}

// GO-ONLY (tiling.ts `#retile`, host_tiling.go `retile`: `page.failure !== undefined` keeps a page out of the cut): a failed page
// that grew past its limit (a patch gave it rows while it was failed) is not cut: the cut pages would carry no failure and the
// list would heal without a run of the page. Pi has no case with a full failed page; the mutant that cuts it is killed here.
// The page is made full by the test (its rows are the list's five, past the bounded limit of 2*first = 4); the real list is
// closed afterwards without a further commit.
func TestTilingAFailedPageThatGrewPastItsLimitIsNotCut(t *testing.T) {
	w, tiling, pages := tgPages(t)
	failed := errors.New("page failure")
	tiling.mu.Lock()
	was := pages[0].rows
	var all []store.StoredRead
	for _, page := range pages {
		all = append(all, page.rows...)
	}
	pages[0].rows, pages[0].failure = all, failed
	tiling.retile()
	kept := slices.Clone(tiling.pages)
	failure := pages[0].failure
	// Back to what the page was, so that the list closes as it was.
	pages[0].rows, pages[0].failure = was, nil
	tiling.mu.Unlock()
	if len(kept) != 3 || kept[0] != pages[0] || !errors.Is(failure, failed) {
		t.Fatalf("pages after retile = %d, first kept = %v, failure = %v: a failed page past its limit must stay as it is, failed",
			len(kept), len(kept) > 0 && kept[0] == pages[0], failure)
	}
	w.close()
}
