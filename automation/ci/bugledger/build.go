// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	fixSubject = regexp.MustCompile(`(?i)^(fix(es|ed)?\b|[\w/().-]+: fix(es|ed)?\b)`)
	prNumber   = regexp.MustCompile(`\(#(\d+)\)`)
	trailerRE  = regexp.MustCompile(`(?m)^Bug-Ledger:\s*(exempt|see)\s+(.+?)\s*$`)
	// shipped excludes paths that never reach a user: tests, evidence, documents, generated records and tooling.
	notShipped = regexp.MustCompile(`(^|/)(testdata|node_modules)/|^(docs|parity|test|tests|changelog\.d|handoffs|plans|automation|\.github|bench|evals|examples)/|^Makefile$|_test\.go$|\.test\.(mjs|ts|js)$|\.(md|mk|json|toml|tsv|txt|ya?ml|lock|sum)$`)
)

func shipped(path string) bool { return !notShipped.MatchString(path) }

// exemptions reads automation/ci/bugledger/exempt.txt: "<commit> <category>: <reason>" lines for fix-titled commits that are not product bugs.
func exemptions(root string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "automation", "ci", "bugledger", "exempt.txt"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for l := range strings.SplitSeq(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		h, reason, ok := strings.Cut(l, " ")
		if !ok || len(h) < 10 || !strings.Contains(reason, ":") && !strings.HasPrefix(reason, "see ") {
			return nil, fmt.Errorf("exempt.txt: %q must be \"<commit> <category>: <reason>\" or \"<commit> see <commit>\"", l)
		}
		out[h] = strings.TrimSpace(reason)
	}
	return out, nil
}

type builder struct {
	r       repo
	root    string
	since   string
	base    string
	idx     testIndex
	order   map[string]int
	entries []*Entry
	rank    map[*Entry]int
	// prior is the committed ledger. Its entries stand in for history this clone lacks; see inherit.
	prior []Entry
	// present holds the 10-character prefix of every commit reachable from HEAD.
	present map[string]bool
	// kept marks the entries copied from prior; their commits and tests are not recomputed.
	kept map[*Entry]bool
	// anchor is the commit that wrote each release note; its message may cite divergences of the other notes it wrote.
	anchor map[*Entry]string
	tests  map[string][]string
	lanes  map[string][]laneCommit
}

// inHistory reports whether the commit named by a short hash is reachable from HEAD.
func (b *builder) inHistory(hash string) bool {
	return len(hash) >= 10 && b.present[hash[:10]]
}

// inherit returns a copy of the committed entry that matches, when that entry names a commit missing from this history. A squash landing replaces the development commits with one commit, so neither the fix commits, their tests, nor the class their messages imply can be recomputed there; the committed entry, generated on the full history, is kept. On the history that holds every commit nothing is inherited and every entry is recomputed.
func (b *builder) inherit(match func(Entry) bool) *Entry {
	for _, p := range b.prior {
		if !match(p) || !slices.ContainsFunc(p.Commits, func(c Commit) bool { return !b.inHistory(c.Hash) }) {
			continue
		}
		e := p
		e.Commits = slices.Clone(p.Commits)
		e.Tests = slices.Clone(p.Tests)
		if e.Tests == nil {
			e.Tests = []string{}
		}
		return &e
	}
	return nil
}

// keptRank orders an inherited entry by the date of its first commit. The development history ordered it by the exact commit time, so regenerating on a squash landing can reorder entries of nearby days; -check without -strict, the CI gate, ignores order.
func keptRank(e *Entry) int {
	if len(e.Commits) == 0 {
		return pendingRank
	}
	t, err := time.Parse("2006-01-02", e.Commits[0].Date)
	if err != nil {
		return pendingRank
	}
	return int(t.Unix())
}

func (b *builder) testsOf(hash, message string) ([]string, error) {
	if t, ok := b.tests[hash]; ok {
		return slices.Clone(t), nil
	}
	t, err := testsOfCommit(b.r, b.idx, hash, message)
	if err != nil {
		return nil, err
	}
	b.tests[hash] = t
	return slices.Clone(t), nil
}

func (b *builder) commitRef(hash string) (Commit, error) {
	ci, err := b.r.commit(hash)
	if err != nil {
		return Commit{}, err
	}
	c := Commit{Hash: ci.Hash[:10], Date: ci.Date}
	if m := prNumber.FindStringSubmatch(ci.Subject); m != nil {
		c.PR = m[1]
	}
	return c, nil
}

func (b *builder) message(hash string) (string, error) {
	ci, err := b.r.commit(hash)
	if err != nil {
		return "", err
	}
	return ci.Subject + "\n\n" + ci.Body, nil
}

func (b *builder) addCommit(e *Entry, hash string) error {
	c, err := b.commitRef(hash)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(e.Commits, func(x Commit) bool { return x.Hash == c.Hash }) {
		e.Commits = append(e.Commits, c)
	}
	return nil
}

func (b *builder) rankOf(hash string) int {
	if n, ok := b.order[hash]; ok {
		return n
	}
	if out, err := b.r.git("show", "-s", "--format=%at", hash); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
			b.order[hash] = n
			return n
		}
	}
	return pendingRank
}

// pendingRank sorts an entry whose fix is not committed after every committed one.
const pendingRank = math.MaxInt

// build assembles the ledger of the repository under root for the releases after since.
func build(root, since string) (*Ledger, error) {
	r := repo{root: root}
	// The ledger reads the history of HEAD. While a merge is in progress the tree already holds the other side's changelog and divergence records but HEAD's history lacks its commits, so a ledger written then misses their fixes and fails the check once the merge is committed.
	if _, merging := r.resolve("MERGE_HEAD"); merging {
		return nil, fmt.Errorf("a merge is in progress: commit the merge first, then regenerate the ledger, which reads the history of HEAD")
	}
	base, err := r.releaseBaseline(since)
	if err != nil {
		return nil, err
	}
	b := &builder{r: r, root: root, since: since, base: base, rank: map[*Entry]int{}, kept: map[*Entry]bool{}, anchor: map[*Entry]string{}, tests: map[string][]string{}, lanes: map[string][]laneCommit{}}
	if b.idx, err = buildTestIndex(root); err != nil {
		return nil, err
	}
	if prior, _, err := readCommitted(root); err == nil {
		b.prior = prior.Entries
	}
	all, err := r.git("rev-list", "HEAD")
	if err != nil {
		return nil, err
	}
	b.present = map[string]bool{}
	for h := range strings.SplitSeq(strings.TrimSpace(all), "\n") {
		if len(h) >= 10 {
			b.present[h[:10]] = true
		}
	}
	list, err := r.git("log", "--no-merges", "--format=%H %at", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	b.order = map[string]int{}
	for l := range strings.SplitSeq(list, "\n") {
		h, at, ok := strings.Cut(l, " ")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(at)
		if err != nil {
			return nil, fmt.Errorf("commit time of %s: %w", h, err)
		}
		b.order[h] = n
	}
	cur, err := currentFixed(root, since)
	if err != nil {
		return nil, err
	}
	first, _, err := introductions(r, base, since)
	if err != nil {
		return nil, err
	}
	if err := b.releaseNoteEntries(cur, first); err != nil {
		return nil, err
	}
	if err := b.checkAttribution(); err != nil {
		return nil, err
	}
	divs := map[string]divergence{}
	divPath := ""
	if p, text, ok := divergenceFile(root); ok {
		divPath, divs = p, parseDivergences(text)
	}
	if err := b.divergenceEntries(divs, divPath); err != nil {
		return nil, err
	}
	exempt, err := b.commitEntries()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(b.entries, func(i, j int) bool { return b.rank[b.entries[i]] < b.rank[b.entries[j]] })
	l := &Ledger{Since: since, Exempt: exempt}
	used := map[string]int{}
	for _, e := range b.entries {
		if b.kept[e] {
			used[e.ID]++
		}
	}
	for i, e := range b.entries {
		if !b.kept[e] {
			e.ID = entryID(e, i, used)
		}
		slices.SortFunc(e.Commits, func(x, y Commit) int { return strings.Compare(x.Date+x.Hash, y.Date+y.Hash) })
		if e.Tests == nil {
			e.Tests = []string{}
		}
		l.Entries = append(l.Entries, *e)
	}
	refs, err := b.divergenceRefs(divs, divPath, l.Entries)
	if err != nil {
		return nil, err
	}
	l.Divergences = refs
	return l, nil
}

// divergenceRefs lists the divergence records added since the baseline, each with the entry of the Pi defect it fixes or none.
func (b *builder) divergenceRefs(divs map[string]divergence, path string, entries []Entry) ([]DivergenceRef, error) {
	entryOf := map[string]string{}
	for _, e := range entries {
		if e.Divergence != "" {
			entryOf[e.Divergence] = e.ID
		}
	}
	var ids []string
	for id := range divs {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, cmpID)
	refs := []DivergenceRef{}
	for _, id := range ids {
		added, err := b.addedSinceBase(path, id)
		if err != nil {
			return nil, err
		}
		if added {
			refs = append(refs, DivergenceRef{ID: id, Title: clip(strings.TrimSuffix(divs[id].Title, "."), 110), Entry: entryOf[id]})
		}
	}
	return refs, nil
}

// locate finds the commit that wrote a release note. A note the fold into CHANGELOG.md reworded matches its fragment by word overlap. A fragment is preferred to the fold because a fragment is written by the commit that fixes the bug.
func (b *builder) locate(first map[string]intro, text string) string {
	if in, ok := first[text]; ok && in.Fragment {
		return in.Hash
	}
	words := wordSet(text)
	best, bestScore := "", 0.0
	for k, in := range first {
		if !in.Fragment || k == text {
			continue
		}
		if s := jaccard(words, wordSet(k)); s > bestScore || (s == bestScore && best != "" && b.rankOf(in.Hash) < b.rankOf(best)) {
			best, bestScore = in.Hash, s
		}
	}
	if bestScore >= 0.6 {
		return best
	}
	if in, ok := first[text]; ok {
		return in.Hash
	}
	return ""
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(strings.ToLower(s), -1) {
		if len(w) >= 3 {
			out[w] = true
		}
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// minRelevance is the score a commit needs to count as work on a release note when its lane carries several.
const minRelevance = 4

func (b *builder) releaseNoteEntries(cur []bullet, first map[string]intro) error {
	var notes []string
	seen := map[string]bool{}
	var uniq []bullet
	for _, bl := range cur {
		if !seen[bl.Text] && bl.Text != "" {
			seen[bl.Text] = true
			uniq = append(uniq, bl)
			notes = append(notes, bl.Text)
		}
	}
	generic := genericTokens(notes)
	claimed := map[string]bool{}
	for _, n := range notes {
		for d := range citedDivergences(n) {
			claimed[d] = true
		}
	}
	located := map[string]string{}
	notesAt := map[string]int{}
	for _, bl := range uniq {
		if len(bl.Fix) == 0 {
			if h := b.locate(first, bl.Text); h != "" {
				located[bl.Text] = h
				notesAt[h]++
			}
		}
	}
	for _, bl := range uniq {
		if e := b.inherit(func(p Entry) bool {
			return p.Origin == "release note" && p.Note == bl.Text && p.FixedIn == bl.Version
		}); e != nil {
			b.entries = append(b.entries, e)
			b.kept[e] = true
			b.rank[e] = keptRank(e)
			continue
		}
		e := &Entry{Note: bl.Text, Origin: "release note", FixedIn: bl.Version, Commits: []Commit{}, Tests: []string{}}
		e.Title = titleOf(bl.Text)
		rank := pendingRank
		var messages, fixMessages []string
		if len(bl.Fix) > 0 {
			if err := b.explicitFix(e, bl.Fix, &messages, &rank); err != nil {
				return err
			}
			fixMessages = messages
		} else if h := located[bl.Text]; h != "" {
			rank = b.rankOf(h)
			if len(h) >= 10 {
				b.anchor[e] = h[:10]
			}
			lane, err := b.laneOf(h)
			if err != nil {
				return err
			}
			fix, testers, fromNote := pickLane(lane, h, bl.Text, generic, claimed, notesAt[h] > 1)
			if fromNote {
				e.CommitsFrom = "release note"
			}
			for _, c := range fix {
				if err := b.addCommit(e, c.Hash); err != nil {
					return err
				}
				messages = append(messages, c.Message)
			}
			// A small lane counts every commit as part of the fix; only a commit that talks about the note decides its class.
			nt := noteTokens(bl.Text)
			for _, c := range fix {
				if !fromNote && relevance(nt, generic, c) >= minRelevance {
					fixMessages = append(fixMessages, c.Message)
				}
			}
			for _, c := range append(slices.Clone(fix), testers...) {
				tests, err := b.testsOf(c.Hash, c.Message)
				if err != nil {
					return err
				}
				for _, t := range tests {
					if !slices.Contains(e.Tests, t) {
						e.Tests = append(e.Tests, t)
					}
				}
			}
			slices.Sort(e.Tests)
		}
		symptom, _ := symptomOf(bl.Text)
		e.Symptom = symptom
		e.Impact = impactOf(bl.Text)
		e.Platform = platformOf(bl.Text)
		e.RootCause, e.CauseFrom = rootCauseOf(messages, bl.Text)
		e.Class = classOf(bl.Text, fixMessages)
		b.entries = append(b.entries, e)
		b.rank[e] = rank
	}
	return nil
}

// classOf is ClassPiPort when the release note says Pi shipped the fix, or a fix commit says it ports or follows a Pi release; otherwise ClassOwn.
func classOf(note string, fixMessages []string) string {
	if piPortRE.MatchString(note) {
		return ClassPiPort
	}
	for _, m := range fixMessages {
		if piPortCommit.MatchString(portHead(m)) {
			return ClassPiPort
		}
	}
	return ClassOwn
}

// explicitFix records the commits a release note names as its fix, with the tests each adds or names.
func (b *builder) explicitFix(e *Entry, names []string, messages *[]string, rank *int) error {
	for _, name := range names {
		full, ok := b.r.resolve(name)
		if !ok {
			return fmt.Errorf("release note %q: the fix marker names %s, which is not a commit", e.Title, name)
		}
		if err := b.addCommit(e, full); err != nil {
			return err
		}
		msg, err := b.message(full)
		if err != nil {
			return err
		}
		*messages = append(*messages, msg)
		tests, err := b.testsOf(full, msg)
		if err != nil {
			return err
		}
		for _, t := range tests {
			if !slices.Contains(e.Tests, t) {
				e.Tests = append(e.Tests, t)
			}
		}
		if n := b.rankOf(full); n < *rank {
			*rank = n
		}
	}
	slices.Sort(e.Tests)
	return nil
}

var mergeRepair = regexp.MustCompile(`(?i)\bmerge\b|\brevert\b|\bregenerate\b`)

// pickLane chooses, from the commits of the lane that carried a release note, the commits that fixed it and the commits whose tests guard it. A fix commit changes shipped code and talks about the note. When no commit of a lane with several commits does, the commit that wrote the note stands in for the fix, and fromNote is true.
//
// A divergence number is an identity, not a word: a commit whose message cites divergences that other notes of the ledger claim, and not this note's, belongs to those notes and never to this one; a note that cites a divergence takes only the commits that cite it. claimed is every divergence number some note cites. shared is true when the commit that wrote the note wrote other notes too, so a short lane says nothing about which of them a commit fixed.
func pickLane(lane []laneCommit, noteCommit, note string, generic map[string]bool, claimed map[string]bool, shared bool) (fix, testers []laneCommit, fromNote bool) {
	nt := noteTokens(note)
	small := len(lane) <= 3 && !shared
	cited := citedDivergences(note)
	if len(cited) > 0 {
		var own []laneCommit
		for _, c := range lane {
			if intersects(citedDivergences(c.Message), cited) {
				own = append(own, c)
			}
		}
		// The commit that wrote the note stays as the anchor even when its message cites nothing.
		for _, c := range lane {
			if c.Hash == noteCommit && !slices.ContainsFunc(own, func(o laneCommit) bool { return o.Hash == c.Hash }) {
				own = append(own, c)
			}
		}
		lane, small = own, false
	}
	var anchor laneCommit
	for _, c := range lane {
		if c.Hash == noteCommit {
			anchor = c
		}
	}
	for _, c := range lane {
		if c.Hash != noteCommit && mergeRepair.MatchString(c.Subject) {
			continue
		}
		if ds := citedDivergences(c.Message); len(cited) == 0 && len(ds) > 0 && claimedElsewhere(ds, claimed) {
			continue
		}
		if len(cited) == 0 && relevance(nt, generic, c) < minRelevance && !small {
			continue
		}
		testers = append(testers, c)
		if c.Shipped {
			fix = append(fix, c)
		}
	}
	if len(fix) == 0 && anchor.Hash != "" {
		fix, fromNote = []laneCommit{anchor}, true
		if !slices.ContainsFunc(testers, func(c laneCommit) bool { return c.Hash == anchor.Hash }) && small {
			testers = append(testers, anchor)
		}
	}
	return fix, testers, fromNote
}

// checkAttribution fails the build when an entry carries a commit of another fix. A divergence number names one fix: an entry whose note cites divergences may carry only commits that cite one of them or none, and an entry whose note cites none may not carry a commit that cites a divergence another note claims.
func (b *builder) checkAttribution() error {
	claimed := map[string]bool{}
	for _, e := range b.entries {
		for d := range citedDivergences(e.Note) {
			claimed[d] = true
		}
	}
	var bad []string
	for _, e := range b.entries {
		if e.Origin != "release note" || b.kept[e] {
			continue
		}
		own := citedDivergences(e.Note)
		for _, c := range e.Commits {
			if !b.inHistory(c.Hash) {
				continue
			}
			msg, err := b.message(c.Hash)
			if err != nil {
				return err
			}
			ds := citedDivergences(msg)
			if len(ds) == 0 || c.Hash == b.anchor[e] {
				continue
			}
			if (len(own) > 0 && !intersects(ds, own)) || (len(own) == 0 && claimedElsewhere(ds, claimed)) {
				bad = append(bad, fmt.Sprintf("%q carries commit %s, which cites %s", clip(e.Note, 60), c.Hash, strings.Join(sortedKeys(ds), ", ")))
			}
		}
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		return fmt.Errorf("cross-attribution: an entry carries a commit of another fix (name the right commits with a <!-- fix: --> marker):\n  %s", strings.Join(bad, "\n  "))
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// citedDivergences returns the divergence numbers a text names.
func citedDivergences(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range divRef.FindAllStringSubmatch(s, -1) {
		out["D"+m[1]] = true
	}
	return out
}

func intersects(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// claimedElsewhere reports whether a divergence the commit cites is cited by some note of the ledger.
func claimedElsewhere(ds, claimed map[string]bool) bool {
	for d := range ds {
		if claimed[d] {
			return true
		}
	}
	return false
}

func (b *builder) divergenceEntries(divs map[string]divergence, path string) error {
	var ids []string
	for id, d := range divs {
		if d.fixesPiDefect() {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		d := divs[id]
		inRange, err := b.addedSinceBase(path, id)
		if err != nil {
			return err
		}
		if !inRange {
			continue
		}
		hashes, err := divergenceCommits(b.r, b.base, id, path)
		if err != nil {
			return err
		}
		// The release note of the fix cites the divergence and no other: class it and keep one entry. A note that cites several divergences, such as a documentation fix that lists them, is not the fix of any one. The earliest such note is the fix; a later one is a follow-up.
		var e *Entry
		for _, x := range b.entries {
			if citesOnly(x.Note, id) && (e == nil || b.rank[x] < b.rank[e]) {
				e = x
			}
		}
		if e == nil {
			if kept := b.inherit(func(p Entry) bool { return p.Origin == "divergence" && p.Divergence == id }); kept != nil {
				b.entries = append(b.entries, kept)
				b.kept[kept] = true
				b.rank[kept] = keptRank(kept)
				continue
			}
			symptom := d.symptom()
			e = &Entry{Origin: "divergence", FixedIn: "Unreleased", Commits: []Commit{}, Tests: []string{}}
			e.Title = clip(strings.TrimSuffix(d.Title, "."), 110)
			e.Symptom = clip(publicText(symptom), maxCause)
			e.Impact = impactOf(symptom)
			e.Platform = platformOf(symptom)
			b.entries = append(b.entries, e)
			b.rank[e] = pendingRank
			if len(hashes) > 0 {
				b.rank[e] = b.rankOf(hashes[0])
			}
		}
		e.Class, e.Divergence = ClassAheadOfPi, id
		e.PiIssue = d.PiIssue
		e.RemoveWhen = d.Remove
		if b.kept[e] {
			continue
		}
		if e.RootCause == "" {
			e.RootCause, e.CauseFrom = clip(publicText(d.Why), maxCause), "divergence record"
		}
		for _, h := range hashes {
			if err := b.addCommit(e, h); err != nil {
				return err
			}
		}
		for _, name := range d.Evidence {
			for _, f := range b.idx[name] {
				ref := f + "::" + name
				if !slices.Contains(e.Tests, ref) {
					e.Tests = append(e.Tests, ref)
				}
			}
		}
		slices.Sort(e.Tests)
	}
	return nil
}

// addedSinceBase reports whether the divergence record id is absent from the baseline release.
func (b *builder) addedSinceBase(path, id string) (bool, error) {
	for _, p := range divergencePaths {
		if out, err := b.r.git("show", b.base+":"+p); err == nil {
			return !strings.Contains(out, "## "+id+" "), nil
		}
	}
	return true, nil
}

// commitEntries adds an entry for every fix-titled commit that changes shipped code and is neither part of an entry nor exempt. It returns the count of exempt commits by category.
func (b *builder) commitEntries() (map[string]int, error) {
	exempt, err := exemptions(b.root)
	if err != nil {
		return nil, err
	}
	covered := map[string]bool{}
	for _, e := range b.entries {
		for _, c := range e.Commits {
			covered[c.Hash] = true
		}
	}
	// A committed commit entry whose commit this clone lacks came from the development history a squash landing replaced; keep it.
	for _, p := range b.prior {
		if p.Origin != "commit" {
			continue
		}
		if e := b.inherit(func(x Entry) bool { return x.Origin == "commit" && x.ID == p.ID }); e != nil {
			b.entries = append(b.entries, e)
			b.kept[e] = true
			b.rank[e] = keptRank(e)
			for _, c := range e.Commits {
				covered[c.Hash] = true
			}
		}
	}
	out, err := b.r.git("log", "--no-merges", "--reverse", "--name-only", "--format=@@%H%x00%s", b.base+"..HEAD")
	if err != nil {
		return nil, err
	}
	type cand struct {
		hash, subject string
		prod          bool
	}
	var cands []cand
	for chunk := range strings.SplitSeq(out, "@@") {
		if chunk == "" {
			continue
		}
		head, files, _ := strings.Cut(chunk, "\n")
		hash, subject, _ := strings.Cut(head, "\x00")
		if !fixSubject.MatchString(subject) {
			continue
		}
		c := cand{hash: hash, subject: subject}
		for f := range strings.SplitSeq(files, "\n") {
			if f != "" && shipped(f) {
				c.prod = true
			}
		}
		cands = append(cands, c)
	}
	counts := map[string]int{}
	// An exempt commit missing from this history was counted where the ledger was generated.
	for h, reason := range exempt {
		if !strings.HasPrefix(reason, "see ") && !b.inHistory(h) {
			category, _, _ := strings.Cut(reason, ":")
			counts[strings.TrimSpace(category)]++
		}
	}
	// A follow-up joins its target's entry after every entry exists: history order does not put a target before its follow-up when the follow-up landed on another branch first.
	type link struct{ target, hash string }
	var links []link
	for _, c := range cands {
		short := c.hash[:10]
		if covered[short] || !c.prod {
			continue
		}
		msg, err := b.message(c.hash)
		if err != nil {
			return nil, err
		}
		if reason := exemptReason(exempt, c.hash, msg); reason != "" {
			if target, ok := strings.CutPrefix(reason, "see "); ok {
				links = append(links, link{strings.TrimSpace(target), c.hash})
				continue
			}
			category, _, _ := strings.Cut(reason, ":")
			counts[strings.TrimSpace(category)]++
			continue
		}
		if target, ok := seeTrailer(msg); ok {
			links = append(links, link{target, c.hash})
			continue
		}
		if landed, err := b.joinLanded(c.hash); err != nil {
			return nil, err
		} else if landed {
			continue
		}
		e := &Entry{Origin: "commit", FixedIn: "Unreleased", Commits: []Commit{}, Tests: []string{}}
		if err := b.addCommit(e, c.hash); err != nil {
			return nil, err
		}
		title := publicText(c.subject)
		if head, _, ok := strings.Cut(c.subject, " ("); title == "" && ok {
			// A subject whose parenthetical carries private wording keeps its public head.
			title = publicText(head)
		}
		if title == "" {
			title = "Fix in shipped code"
		}
		e.Title, e.Symptom = clip(title, 110), title
		e.Impact, e.Platform, e.Class = impactOf(title), platformOf(title), ClassOwn
		if e.Tests, err = b.testsOf(c.hash, msg); err != nil {
			return nil, err
		}
		e.RootCause, e.CauseFrom = rootCauseOf([]string{msg}, "")
		if piPortCommit.MatchString(portHead(msg)) {
			e.Class = ClassPiPort
		}
		b.entries = append(b.entries, e)
		b.rank[e] = b.rankOf(c.hash)
	}
	for _, l := range links {
		if err := b.linkCommit(l.target, l.hash); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

// ledgerHashLine matches a commit hash of an entry in the JSON ledger.
var ledgerHashLine = regexp.MustCompile(`^\s*"hash":\s*"([0-9a-f]{10})"`)

// joinLanded adds commit hash to every inherited entry that hash wrote a commit of into the committed ledger, and reports whether it found one. A squash merge replaces the commits of a pull request with one commit titled after the pull request. That commit carries the ledger the pull request regenerated, which names the pull request's own commits, so the squash commit is the landing of those entries, not a bug without one. Pull request checks run before the squash, so without this every fix-titled squash merge would fail the check of the branch it lands on.
func (b *builder) joinLanded(hash string) (bool, error) {
	diff, err := b.r.git("show", "--format=", "-U0", hash, "--", jsonPath)
	if err != nil {
		return false, err
	}
	written := map[string]bool{}
	for l := range strings.SplitSeq(diff, "\n") {
		if rest, ok := strings.CutPrefix(l, "+"); ok {
			if m := ledgerHashLine.FindStringSubmatch(rest); m != nil {
				written[m[1]] = true
			}
		}
	}
	if len(written) == 0 {
		return false, nil
	}
	joined := false
	for _, e := range b.entries {
		if b.kept[e] && slices.ContainsFunc(e.Commits, func(c Commit) bool { return written[c.Hash] }) {
			if err := b.addCommit(e, hash); err != nil {
				return false, err
			}
			joined = true
		}
	}
	return joined, nil
}

func exemptReason(exempt map[string]string, hash, msg string) string {
	for prefix, reason := range exempt {
		if strings.HasPrefix(hash, prefix) {
			return reason
		}
	}
	for _, m := range trailerRE.FindAllStringSubmatch(msg, -1) {
		if m[1] == "exempt" && strings.Contains(m[2], ":") {
			return m[2]
		}
	}
	return ""
}

// seeTrailer returns the commit a "Bug-Ledger: see <commit>" trailer names.
func seeTrailer(msg string) (string, bool) {
	for _, m := range trailerRE.FindAllStringSubmatch(msg, -1) {
		if m[1] == "see" {
			return m[2], true
		}
	}
	return "", false
}

// linkCommit adds commit hash to the entry that lists the commit named target.
func (b *builder) linkCommit(target, hash string) error {
	full, ok := b.r.resolve(target)
	if !ok {
		return fmt.Errorf("commit %s: %q names no commit", hash[:10], target)
	}
	for _, e := range b.entries {
		for _, c := range e.Commits {
			if strings.HasPrefix(full, c.Hash) {
				return b.addCommit(e, hash)
			}
		}
	}
	return fmt.Errorf("commit %s: %s has no ledger entry to join", hash[:10], target)
}

// entryID names an entry after the commit that first fixed it, so an ID survives merges and later entries. A divergence entry takes its divergence number. An entry with no commit yet is "BF-pending-<n>".
func entryID(e *Entry, i int, used map[string]int) string {
	id := fmt.Sprintf("BF-pending-%d", i+1)
	switch {
	case e.Divergence != "" && e.Origin == "divergence":
		id = "BF-" + e.Divergence
	case len(e.Commits) > 0:
		id = "BF-" + e.Commits[0].Hash[:7]
	}
	used[id]++
	if used[id] > 1 {
		id = fmt.Sprintf("%s-%d", id, used[id])
	}
	return id
}
