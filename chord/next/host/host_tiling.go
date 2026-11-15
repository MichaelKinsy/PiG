package host

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/host/tiling.ts
//
// The pages of a list for one target (a query and its arguments): one open query, a read of it per page, the rows each
// returned, and the tiling rules (slice 14 §4).
//
// Go forms: `after` and `through` are nil Positions where Pi has `undefined`; a page's `failure` is the error, nil where Pi
// has `undefined`; Pi's one thread is `mu` over the pages (never held across a call to the owner); Pi's `void read.load()`
// is a task of the list's loop (`spawn`: the reader site's Go), which the loop's join counts; Pi's `await Promise.all(loads)` is a
// task per load, the caller parked until the last ended.

// tilingPage is one page: the rows after `after` through `through` (the last page has none: it is open at its end).
type tilingPage struct {
	after   store.Position
	through store.Position
	read    *store.OpenRead
	// rows are as its last run (or the cut that made it) left them; nil until its first run.
	rows []store.StoredRead
	// ran is whether rows is set (Pi's `rows !== undefined`: a run of no rows is not a page that has not run).
	ran bool
	// more is whether rows of its range are unread behind `rows`: its run was cut by its limit.
	more bool
	// failure is why its last run could not read it, until a run of it succeeds.
	failure error
}

// maxPages is how many rows a bounded page reads at most, in pages (slice 14 §4 "Overflow").
const maxPages = 4

// tilingOwner is who a tiling tells: its list. Each call gives the tiling (Pi's owner is an object literal over it): a
// binding may change while the tiling is being made.
type tilingOwner interface {
	// adopted: a run was adopted; called inside a batch.
	adopted(t *tiling)
	// rebound: a binding of its query changed (a set moved, its table ready after a migration): rows and their order may
	// be others in every page. Its pages are not read again one by one (the first adoption would cut and merge the others
	// with the rows they had); its list reads the target again as one new tiling.
	rebound(t *tiling)
}

// tiling is the pages of a list for one target.
type tiling struct {
	target store.LiveTarget
	query  *store.OpenQuery
	first  int
	order  store.Order
	owner  tilingOwner
	// spawn runs a task of the loop the list lives on (the reader site's Go).
	spawn func(task func())

	// mu guards pages. Lock order, where this lock meets the store's: mu first, then the reads index (`Reads.mu`, a leaf the
	// calls a page makes under mu take: `read.Close`, `Know`, `Forget`, the reads a retile opens), never the reverse: the
	// index calls out (a sink's `Adopt`, which is `adopt` here, a watcher, a query's `Close`) only after it released its
	// lock, so nothing holds the index and waits for mu. No call to the owner is made under mu.
	mu    sync.Mutex
	pages []*tilingPage
}

func newTiling(site store.ReadSite, target store.LiveTarget, first int, owner tilingOwner, ctx context.Context, spawn func(task func())) *tiling {
	t := &tiling{target: target, first: first, order: target.Spec.Query.Order(), owner: owner, spawn: spawn}
	t.query = store.NewOpenQuery(site, target.Spec, store.ReadRows, func() { owner.rebound(t) }, ctx)
	return t
}

// signature is what bookmarks of this tiling are positions of: the table, the order's columns and its direction.
func (t *tiling) signature() string {
	// JSON.stringify([table, columns, direction]): strings as the port's JSON string marshaller escapes them.
	var text strings.Builder
	text.WriteString("[" + jsStringify(t.target.Spec.Table) + ",[")
	for index, column := range t.order.Columns {
		if index > 0 {
			text.WriteByte(',')
		}
		text.WriteString(jsStringify(column))
	}
	text.WriteString("]," + jsStringify(t.order.Direction) + "]")
	return text.String()
}

// bookmarks are the upper bound of every bounded page.
func (t *tiling) bookmarks() []store.Position {
	t.mu.Lock()
	defer t.mu.Unlock()
	var marks []store.Position
	for _, page := range t.pages {
		if page.through != nil {
			marks = append(marks, page.through)
		}
	}
	return marks
}

// open is whether the last page is open at its end: no row is known behind what the list shows.
func (t *tiling) open() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.openLocked()
}

func (t *tiling) openLocked() bool {
	return len(t.pages) == 0 || t.pages[len(t.pages)-1].through == nil
}

// moreAfter is whether rows may follow what `rows` gives: a bounded last page, or a page that has not run yet.
func (t *tiling) moreAfter() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.openLocked() || !t.completeLocked()
}

// complete is whether every page has run.
func (t *tiling) complete() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.completeLocked()
}

func (t *tiling) completeLocked() bool {
	return !slices.ContainsFunc(t.pages, func(page *tilingPage) bool { return !page.ran })
}

// failure is the failure of the first page whose last run failed: the list is failed while any page is.
func (t *tiling) failure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, page := range t.pages {
		if page.failure != nil {
			return page.failure
		}
	}
	return nil
}

// retired is whether its query was closed under it: its provider's attempt ended (slice 14 §3.2). It keeps its rows and
// loads nothing; the list reads the successor's query next.
func (t *tiling) retired() bool { return t.query.Closed() }

// context is the context its rows are decoded under: its query's.
func (t *tiling) context() context.Context { return t.query.Ctx }

// rows are the loaded rows in order, up to the first page that has none yet: a prefix, never a hole.
func (t *tiling) rows() []store.StoredRead {
	t.mu.Lock()
	defer t.mu.Unlock()
	var rows []store.StoredRead
	for _, page := range t.pages {
		if !page.ran {
			break
		}
		rows = append(rows, page.rows...)
	}
	return rows
}

// openPages opens a page per bookmark, and the open last page when the list ended in one: their reads are registered, nothing is
// loaded (Pi's `start` up to its first await). It returns the load, which returns once each page's load settled. No
// bookmarks: one open page, the first.
func (t *tiling) openPages(bookmarks []store.Position, open bool) (load func()) {
	t.mu.Lock()
	var after store.Position
	for _, through := range bookmarks {
		t.pages = append(t.pages, t.page(after, through))
		after = through
	}
	if open || len(bookmarks) == 0 {
		t.pages = append(t.pages, t.page(after, nil))
	}
	pages := slices.Clone(t.pages)
	t.mu.Unlock()
	return func() {
		// `await Promise.all(pages.map((page) => page.read.load()))` (tiling.ts:127): a task of the loop per page's load; the caller
		// waits parked (parkedOn: the join counts the loads itself). A page's load that panics rejects the whole, which is the
		// panic again in the caller, once every load ended.
		if len(pages) == 0 {
			return
		}
		done := make(chan struct{})
		var mu sync.Mutex
		remaining, panicked := len(pages), any(nil)
		for _, page := range pages {
			t.spawn(func() {
				defer func() {
					recovered := recover()
					mu.Lock()
					if recovered != nil && panicked == nil {
						panicked = recovered
					}
					remaining--
					last := remaining == 0
					mu.Unlock()
					if last {
						close(done)
					}
				}()
				page.read.Load()
			})
		}
		parkedOn(done)
		mu.Lock()
		failure := panicked
		mu.Unlock()
		if failure != nil {
			panic(failure)
		}
	}
}

// more loads the next page behind a bounded last page; with an open last page there is nothing more to load.
func (t *tiling) more() {
	t.mu.Lock()
	if len(t.pages) == 0 || t.pages[len(t.pages)-1].through == nil {
		t.mu.Unlock()
		return
	}
	page := t.page(t.pages[len(t.pages)-1].through, nil)
	t.pages = append(t.pages, page)
	t.mu.Unlock()
	page.read.Load()
}

// close closes its query: no commit runs its pages again.
func (t *tiling) close() {
	t.query.Close()
	t.mu.Lock()
	t.pages = nil
	t.mu.Unlock()
}

// load is Pi's `void read.load()` (tiling.ts:211, :259): a task of the loop the list lives on, which the loop's join waits for.
func (t *tiling) load(read *store.OpenRead) {
	t.spawn(func() { read.Load() })
}

// page is a page of `(after, through]` that has not run: its read is registered, a load or a commit runs it. Called with
// mu held.
func (t *tiling) page(after, through store.Position) *tilingPage {
	page := &tilingPage{after: after, through: through}
	sink := adoptFunc(func(run store.ReadRun) { t.adopt(page, run) })
	page.read = t.query.Read(store.ReadKind{Kind: "rows", Range: t.rangeOf(after, through)}, sink)
	return page
}

// pageOf is a page of `(after, through]` whose rows are known (a cut, a merge): its read runs nothing. Called with mu held.
func (t *tiling) pageOf(after, through store.Position, rows []store.StoredRead) *tilingPage {
	page := &tilingPage{after: after, through: through, rows: rows, ran: true}
	sink := adoptFunc(func(run store.ReadRun) { t.adopt(page, run) })
	page.read = t.query.ReadKnown(t.rangeOf(after, through), rows, sink)
	return page
}

// adoptFunc is a ReadSink of a function.
type adoptFunc func(run store.ReadRun)

func (f adoptFunc) Adopt(run store.ReadRun) { f(run) }

// rangeOf is the range a page of `(after, through]` reads.
func (t *tiling) rangeOf(after, through store.Position) store.Range {
	// The open last page reads one page; a bounded one everything between its bookmarks, up to the overflow bound.
	limit := t.first
	if through != nil {
		limit = t.first * maxPages
	}
	return store.Range{After: after, Through: through, Limit: limit}
}

// adopt takes a page's run, inside a batch. A run belongs to the interval it read, not to a page (slice 14 §4): the runs of
// one commit, and of a commit group, are all computed before any is adopted, and an earlier adoption may have cut or
// merged the page meanwhile. The run then applies to whatever pages cover its interval now.
func (t *tiling) adopt(page *tilingPage, run store.ReadRun) {
	t.mu.Lock()
	current := slices.Contains(t.pages, page)
	if run.Kind != "rows" {
		// A page's read is never a count. The pages that show the interval are failed until a run of them succeeds; a page
		// that did not run itself forgets its result, so the next commit over its tables runs it.
		failure := run.Err
		if run.Kind != "failed" {
			failure = errors.New("a list's page took a count")
		}
		each := []*tilingPage{page}
		if !current {
			each = slices.DeleteFunc(slices.Clone(t.pages), func(other *tilingPage) bool { return !t.overlap(other, page) })
		}
		for _, one := range each {
			one.failure = failure
			one.read.Forget()
		}
		t.mu.Unlock()
		t.owner.adopted(t)
		return
	}
	if current {
		page.rows, page.ran, page.more, page.failure = run.Result.Rows, true, run.Result.More, nil
	} else {
		t.patch(page, run.Result.Rows, run.Result.More)
	}
	t.retile()
	t.mu.Unlock()
	t.owner.adopted(t)
}

// patch applies the run of a retired page (`rows` of its interval; `more`: the run was cut by its limit, so the interval is
// known only through its last row) to the pages that cover the interval now. Each patched page's read is told its rows
// again: the skip test reasons from the keys of what the page shows. A failed page takes the rows and stays without a
// result, so the next commit over its tables runs it, whatever it writes. A page that lies behind what a cut run knows is
// loaded again; one the cut falls into keeps the rows up to it, and `retile` continues behind. Called with mu held.
func (t *tiling) patch(retired *tilingPage, rows []store.StoredRead, more bool) {
	var known store.Position
	if more {
		known = t.position(rows[len(rows)-1])
	}
	for index, page := range t.pages {
		if !page.ran || !t.overlap(page, retired) {
			continue
		}
		if known != nil && page.after != nil && t.compare(page.after, known) >= 0 {
			page.read.Close()
			again := t.page(page.after, page.through)
			t.load(again.read)
			t.pages[index] = again
			continue
		}
		var merged []store.StoredRead
		for _, row := range page.rows {
			if !t.inside(t.position(row), retired) {
				merged = append(merged, row)
			}
		}
		for _, row := range rows {
			if t.inside(t.position(row), page) {
				merged = append(merged, row)
			}
		}
		slices.SortStableFunc(merged, func(a, b store.StoredRead) int { return t.compare(t.position(a), t.position(b)) })
		cut := known != nil && (page.through == nil || t.compare(page.through, known) > 0)
		if cut {
			merged = slices.DeleteFunc(merged, func(row store.StoredRead) bool { return t.compare(t.position(row), known) > 0 })
		}
		page.rows = merged
		page.more = page.more || cut
		if page.failure == nil {
			page.read.Know(page.rows)
		}
	}
}

// retile cuts and merges, from the rows the pages hold, with no I/O. The open last page with a row behind it, or grown past
// one page, gets its bound at its last row (the list ends there until `more`); a bounded page past the overflow bound is
// cut and the rest of its range loads next; a page of more than two pages' rows is cut into pages; neighbours with at most
// one page's rows together become one. Called with mu held.
func (t *tiling) retile() {
	first := t.first
	var pages []*tilingPage
	for _, page := range t.pages {
		rows := page.rows
		open := page.through == nil
		limit := 2 * first
		if open {
			limit = first
		}
		full := page.ran && (page.more || len(rows) > limit)
		if !page.ran || page.failure != nil || !full {
			pages = append(pages, page)
			continue
		}
		page.read.Close()
		// Rows of a bounded page's range are still unread behind `rows` (overflow): the rest loads as a page of its own.
		unread := !open && page.more
		through := page.through
		if open {
			through = t.position(rows[len(rows)-1])
		}
		after := page.after
		for from := 0; from < len(rows); from += first {
			// The last cut takes what is left when that is at most two pages' rows, and ends where the page did.
			last := len(rows)-from <= 2*first
			var chunk []store.StoredRead
			if last {
				chunk = rows[from:]
			} else {
				chunk = rows[from : from+first]
			}
			end := t.position(chunk[len(chunk)-1])
			if last && !unread {
				end = through
			}
			pages = append(pages, t.pageOf(after, end, chunk))
			after = end
			if last {
				break
			}
		}
		if unread {
			rest := t.page(after, page.through)
			pages = append(pages, rest)
			t.load(rest.read)
		}
	}
	t.pages = t.merged(pages)
}

// merged is pages with neighbours that hold at most one page's rows together merged into one.
func (t *tiling) merged(pages []*tilingPage) []*tilingPage {
	var merged []*tilingPage
	for _, page := range pages {
		if len(merged) == 0 || !mergeable(merged[len(merged)-1]) || !mergeable(page) {
			merged = append(merged, page)
			continue
		}
		previous := merged[len(merged)-1]
		rows := append(slices.Clone(previous.rows), page.rows...)
		if len(rows) > t.first {
			merged = append(merged, page)
			continue
		}
		previous.read.Close()
		page.read.Close()
		merged[len(merged)-1] = t.pageOf(previous.after, page.through, rows)
	}
	return merged
}

// mergeable is whether `page` may merge with a neighbour: every row of its range is known.
func mergeable(page *tilingPage) bool { return page.ran && !page.more && page.failure == nil }

// overlap is whether the intervals of two pages share a position.
func (t *tiling) overlap(a, b *tilingPage) bool {
	before := func(x, y *tilingPage) bool {
		return x.through != nil && y.after != nil && t.compare(x.through, y.after) <= 0
	}
	return !before(a, b) && !before(b, a)
}

// inside is whether `position` is in `page`'s interval.
func (t *tiling) inside(position store.Position, page *tilingPage) bool {
	if page.after != nil && t.compare(position, page.after) <= 0 {
		return false
	}
	return page.through == nil || t.compare(position, page.through) <= 0
}

// position is the position of `row` in the order: its order columns' values.
func (t *tiling) position(row store.StoredRead) store.Position {
	position := make(store.Position, len(t.order.Columns))
	for index, name := range t.order.Columns {
		position[index] = row[name]
	}
	return position
}

// compare orders two positions as the list shows them: negative when `a` comes first.
func (t *tiling) compare(a, b store.Position) int {
	for index := range a {
		// Order columns are non-`opt` `str` or `int` ones (checked when the query was built).
		order := store.CompareScalar(a[index], b[index])
		if order != 0 {
			if t.order.Direction == "asc" {
				return order
			}
			return -order
		}
	}
	return 0
}
