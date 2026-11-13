package docsdrift

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	divergencePage         = "internal/pigdocs/content/divergences.md"
	divergenceTableHeading = "## Behavior differences"
)

var (
	activeDivergenceHeading = regexp.MustCompile(`(?m)^## (D\d+) `)
	divergenceRow           = regexp.MustCompile(`^\|\s*(D\d+)\s*\|`)
)

// The bundle's divergence page is what an agent reads before it assumes Pi
// behavior. Every active divergence in the ledger must appear there, once, as a row of the one table under
// "## Behavior differences". The page is edited by hand: a row appended at the end of the file, or after a blank
// line a merge left, is in no table a Markdown reader draws, and the same id has been given two rows with two wordings.
func TestEveryActiveDivergenceIsInTheBundle(t *testing.T) {
	ledger, err := os.ReadFile("../../docs/parity/DIVERGENCES.md")
	if err != nil {
		t.Fatal(err)
	}
	page := docFiles(t)["divergences.md"]
	matches := activeDivergenceHeading.FindAllStringSubmatch(string(ledger), -1)
	if len(matches) == 0 {
		t.Fatal("no divergence IDs parsed from docs/parity/DIVERGENCES.md; the gate would pass vacuously")
	}
	active := make([]string, 0, len(matches))
	for _, match := range matches {
		active = append(active, match[1])
	}
	for _, problem := range divergenceTableProblems(page, active) {
		t.Error(problem)
	}
}

// divergenceTableProblems compares the divergence page of the docs bundle with the ids of the active records. The
// table is the "| ID |" header row and its "|---" row under the heading, then every line that follows and starts
// with "|": a blank line or any other text ends it.
func divergenceTableProblems(page string, active []string) []string {
	lines := strings.Split(page, "\n")
	first, end := 0, 0 // the rows are lines[first:end]
	for i, line := range lines {
		if line != divergenceTableHeading {
			continue
		}
		header := i + 1
		for header < len(lines) && strings.TrimSpace(lines[header]) == "" {
			header++
		}
		if header+1 < len(lines) && strings.HasPrefix(lines[header], "| ID |") && strings.HasPrefix(lines[header+1], "|---") {
			first = header + 2
			end = first
			for end < len(lines) && strings.HasPrefix(lines[end], "|") {
				end++
			}
		}
		break
	}
	if first == 0 {
		return []string{fmt.Sprintf("%s: no table (an \"| ID |\" header row and its \"|---\" row) under %q", divergencePage, divergenceTableHeading)}
	}

	var problems []string
	rowLines := map[string][]string{} // id: the line numbers of its rows
	var ids []string                  // in page order, each once
	var stray []string                // the ids of a run of rows outside the table
	strayLine := 0
	endStray := func() {
		if len(stray) == 0 {
			return
		}
		rows := "1 row (" + stray[0] + ")"
		if len(stray) > 1 {
			rows = fmt.Sprintf("%d rows (%s to %s)", len(stray), stray[0], stray[len(stray)-1])
		}
		problems = append(problems, fmt.Sprintf("%s:%d: %s outside the table under %q, which ends at line %d: a row belongs in that table, with no blank line and no other text between rows",
			divergencePage, strayLine, rows, divergenceTableHeading, end))
		stray = nil
	}
	for i, line := range lines {
		inTable := i >= first && i < end
		match := divergenceRow.FindStringSubmatch(line)
		if match == nil {
			endStray()
			if inTable {
				problems = append(problems, fmt.Sprintf("%s:%d: a table row without a divergence id: %.80q", divergencePage, i+1, line))
			}
			continue
		}
		id := match[1]
		if len(rowLines[id]) == 0 {
			ids = append(ids, id)
		}
		rowLines[id] = append(rowLines[id], strconv.Itoa(i+1))
		if inTable {
			endStray()
			continue
		}
		if len(stray) == 0 {
			strayLine = i + 1
		}
		stray = append(stray, id)
	}
	endStray()

	isActive := make(map[string]bool, len(active))
	for _, id := range active {
		isActive[id] = true
	}
	for _, id := range ids {
		if n := len(rowLines[id]); n > 1 {
			problems = append(problems, fmt.Sprintf("%s: %s has %d rows (lines %s): keep the one that matches its record", divergencePage, id, n, strings.Join(rowLines[id], ", ")))
		}
		if !isActive[id] {
			problems = append(problems, fmt.Sprintf("%s:%s: %s has a row but docs/parity/DIVERGENCES.md has no \"## %s\" heading: a retired or unrecorded divergence is not listed",
				divergencePage, rowLines[id][0], id, id))
		}
	}
	for _, id := range active {
		if len(rowLines[id]) == 0 {
			problems = append(problems, fmt.Sprintf("%s does not list active divergence %s: add a \"| %s | title | summary |\" row as the last row of the table under %q",
				divergencePage, id, id, divergenceTableHeading))
		}
	}
	return problems
}

// Each defect is named on a small page of its own, so a later edit of divergenceTableProblems cannot stop seeing one
// while the real page happens to be sound.
func TestDivergenceTableProblemsNameEachDefect(t *testing.T) {
	const (
		head = "# Divergences\n\n## Behavior differences\n\n| ID | Title | Summary |\n|---|---|---|\n" // rows start at line 7
		d1   = "| D1 | one | first |\n"
		d2   = "| D2 | two | second |\n"
		d3   = "| D3 | three | third |\n"
		tail = "\n## When to write a new divergence\n\n- A bullet.\n"
	)
	active := []string{"D1", "D2", "D3"}
	cases := []struct {
		name string
		page string
		want []string // one substring for each problem, in order
	}{
		{"sound page", head + d1 + d2 + d3 + tail, nil},
		{"blank line inside the table", head + d1 + "\n" + d2 + d3 + tail, []string{":9: 2 rows (D2 to D3) outside the table under \"## Behavior differences\", which ends at line 7"}},
		{"text between rows", head + d1 + d2 + "A sentence.\n" + d3 + tail, []string{":10: 1 row (D3) outside the table"}},
		{"row after the last section", head + d1 + d2 + tail + d3, []string{":13: 1 row (D3) outside the table"}},
		{"two separate runs outside", head + d1 + "\n" + d2 + tail + d3, []string{":9: 1 row (D2) outside", ":14: 1 row (D3) outside"}},
		{"an id twice", head + d1 + d2 + d3 + "| D2 | two | other words |\n" + tail, []string{"D2 has 2 rows (lines 8, 10)"}},
		{"a row without a record", head + d1 + d2 + d3 + "| D4 | four | fourth |\n" + tail, []string{":10: D4 has a row but docs/parity/DIVERGENCES.md has no \"## D4\" heading"}},
		{"a record without a row", head + d1 + d2 + tail, []string{"does not list active divergence D3"}},
		{"a table row that is no divergence", head + d1 + d2 + "| see below | | |\n" + d3 + tail, []string{":9: a table row without a divergence id"}},
		{"no table under the heading", "# Divergences\n\n## Behavior differences\n\n" + d1 + d2 + d3 + tail, []string{"no table"}},
		{"table under another heading", "# Divergences\n\n## Other\n\n| ID | Title | Summary |\n|---|---|---|\n" + d1 + d2 + d3 + tail, []string{"no table"}},
	}
	for _, c := range cases {
		got := divergenceTableProblems(c.page, active)
		if len(got) != len(c.want) {
			t.Errorf("%s: %d problems, want %d:\n%s", c.name, len(got), len(c.want), strings.Join(got, "\n"))
			continue
		}
		for i, want := range c.want {
			if !strings.Contains(got[i], want) {
				t.Errorf("%s: problem %d is %q, want it to contain %q", c.name, i, got[i], want)
			}
		}
	}
}

// docs/parity/DIVERGENCES.md is generated from the one-file-per-record sources in docs/parity/divergences. A record
// edited without regenerating, or a ledger edited by hand, leaves the two different.
func TestDivergenceLedgerEqualsItsRecordFiles(t *testing.T) {
	cmd := exec.Command("go", "run", "./automation/ci/divsplit", "-check")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
