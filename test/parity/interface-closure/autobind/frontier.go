package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// frontierRow is one root gap of the ledger with the child-gap rows it holds open.
type frontierRow struct {
	ID, Pkg, Kind, Source, PiSrc, Candidate, Detail string
	// Alone counts the child-gap rows this gap is the only open root gap of: closing it closes them. Total counts every child-gap row
	// that has this gap among its blockers.
	Alone, Total int
}

// frontier ranks the root gaps by the child-gap rows they hold open. A root gap is a gap whose reason is its own, not derived from another
// row. A child-gap row is blocked by the root gaps in its subtree (a parent of a gap member) and by the root gap that owns it (a member
// below a gap declaration).
func frontier(l *ledger, ds []*decision) []frontierRow {
	byID := make(map[string]*decision, len(ds))
	for _, d := range ds {
		byID[d.ID] = d
	}
	root := func(id string) bool {
		d := byID[id]
		return d != nil && d.Gap && d.Reason != reasonChild && d.Reason != reasonCLI
	}
	// below[id] is the set of root gaps strictly under id.
	below := map[string]map[string]bool{}
	var collect func(id string) map[string]bool
	collect = func(id string) map[string]bool {
		if s, ok := below[id]; ok {
			return s
		}
		s := map[string]bool{}
		below[id] = s
		for _, ch := range l.children[id] {
			if root(ch.ID) {
				s[ch.ID] = true
			}
			for r := range collect(ch.ID) {
				s[r] = true
			}
		}
		return s
	}
	blockers := func(id string) map[string]bool {
		b := map[string]bool{}
		for r := range collect(id) {
			b[r] = true
		}
		for e := l.byID[id]; e != nil && e.ParentID != ""; e = l.byID[e.ParentID] {
			if root(e.ParentID) {
				b[e.ParentID] = true
			}
		}
		return b
	}
	stats := map[string]*frontierRow{}
	for _, d := range ds {
		if !root(d.ID) {
			continue
		}
		row := &frontierRow{ID: d.ID, Pkg: d.Pkg, Kind: d.Reason, Candidate: d.Candidate, Detail: strings.Join(strings.Fields(d.Detail), " ")}
		if e := l.byID[d.ID]; e != nil && e.SourcePath != "" {
			row.Source = fmt.Sprintf("%s:%d", e.SourcePath, e.SourceLine)
		}
		stats[d.ID] = row
	}
	for _, d := range ds {
		if !d.Gap || d.Reason != reasonChild {
			continue
		}
		b := blockers(d.ID)
		for r := range b {
			if row := stats[r]; row != nil {
				row.Total++
				if len(b) == 1 {
					row.Alone++
				}
			}
		}
	}
	out := make([]frontierRow, 0, len(stats))
	for _, r := range stats {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Alone != b.Alone:
			return a.Alone > b.Alone
		case a.Total != b.Total:
			return a.Total > b.Total
		}
		return a.ID < b.ID
	})
	return out
}

// frontierTSV renders the ranking: rank, ID, package, kind, rows unblocked alone, rows held in total, the Pi declaration file:line the
// inventory records (the published .d.ts, or the source file for a package the inventory reads from source), the Pi source file:line found by name (blank when the source has no such declaration), the
// closest Go symbol and the detail.
func frontierTSV(rows []frontierRow, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# root gaps ranked by the child-gap rows they unblock; %d child-gap rows in all\n", total)
	b.WriteString("rank\tid\tpackage\tkind\tunblocks_alone\tblocks_total\tpi_decl_file_line\tpi_src_file_line\tgo_candidate\tdetail\n")
	for i, r := range rows {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\n", i+1, r.ID, r.Pkg, r.Kind, r.Alone, r.Total, r.Source, r.PiSrc, r.Candidate, r.Detail)
	}
	return b.String()
}

// writeFrontier writes the ranking atomically, so a lane reading it never sees a partial file.
func writeFrontier(path, root string, l *ledger, ds []*decision) error {
	rows := frontier(l, ds)
	src := piSources{root: root, files: map[string][]string{}}
	for i := range rows {
		rows[i].PiSrc = src.locate(l.byID[rows[i].ID], rows[i].Pkg)
	}
	children := 0
	for _, d := range ds {
		if d.Gap && d.Reason == reasonChild {
			children++
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + fmt.Sprintf(".tmp%d", os.Getpid())
	if err := os.WriteFile(tmp, []byte(frontierTSV(rows, children)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// piSources finds a declaration in the pinned Pi source tree.
type piSources struct {
	root  string
	files map[string][]string // repo-relative path -> lines; nil when unreadable
}

// locate maps the declaration the inventory records in a published .d.ts (node_modules/@scope/pi-x/dist/a/b.d.ts) to the pinned source
// (.upstream/current/packages/<key>/src/a/b.ts) and returns "packages/<key>/src/a/b.ts:<line>": the line of the member inside its owner's
// declaration, or of the owner's declaration when the owner's block does not name the member (an inherited member). The result is blank when the source file or the declaration is not there; a .d.ts line is
// not a source line, so no line is guessed.
func (s *piSources) locate(e *upstreamEntry, pkgKey string) string {
	if e == nil || pkgKey == "" {
		return ""
	}
	if strings.HasPrefix(e.SourcePath, "packages/") && strings.HasSuffix(e.SourcePath, ".ts") && e.SourceLine > 0 {
		// The inventory already records the source declaration (packages read from source, not from a published .d.ts).
		return fmt.Sprintf("%s:%d", e.SourcePath, e.SourceLine)
	}
	_, rel, ok := strings.Cut(e.SourcePath, "/dist/")
	if !ok {
		return ""
	}
	rel = "packages/" + pkgKey + "/src/" + strings.TrimSuffix(rel, ".d.ts") + ".ts"
	lines, seen := s.files[rel]
	if !seen {
		data, err := os.ReadFile(filepath.Join(s.root, ".upstream", "current", filepath.FromSlash(rel)))
		if err == nil {
			lines = strings.Split(string(data), "\n")
		}
		s.files[rel] = lines
	}
	if lines == nil {
		return ""
	}
	parts := parseID(e.ID)
	declLine := func(from int, re *regexp.Regexp) int {
		for i := from; i < len(lines); i++ {
			if re.MatchString(lines[i]) {
				return i + 1
			}
		}
		return 0
	}
	owner := regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?(?:abstract\s+)?(?:interface|class|type|function|const|let|var|enum)\s+` + regexp.QuoteMeta(parts.Name) + `\b`)
	line := declLine(0, owner)
	if line == 0 {
		return ""
	}
	if parts.Member != "" {
		// The member must lie inside the owner's block, which closes at the next line that starts with "}".
		end := len(lines)
		for i := line; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "}") {
				end = i + 1
				break
			}
		}
		member := regexp.MustCompile(`\b` + regexp.QuoteMeta(parts.Member) + `\b`)
		for i := line; i < end; i++ {
			if member.MatchString(lines[i]) {
				line = i + 1
				break
			}
		}
	}
	return fmt.Sprintf("%s:%d", rel, line)
}
