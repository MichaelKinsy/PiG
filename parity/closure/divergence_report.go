package closure

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const DivergenceDashboardDataset = "divergence"

type divergenceReportRow struct {
	id       string
	number   int
	title    string
	scrutiny string
	state    VerdictState
	reason   string
}

func renderDivergenceDashboard(graph *Graph) ([]byte, error) {
	claims, err := reportClaims(graph, DivergenceDashboardDataset, DivergenceDashboardDataset)
	if err != nil {
		return nil, err
	}
	rows := make([]divergenceReportRow, 0)
	seen := make(map[string]struct{})
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		if fact.FactType != "denominator:divergence" {
			continue
		}
		if _, duplicate := seen[fact.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate divergence %s", DivergenceDashboardDataset, fact.SubjectID)
		}
		seen[fact.SubjectID] = struct{}{}
		numberText, ok := strings.CutPrefix(fact.SubjectID, "D")
		if !ok {
			return nil, fmt.Errorf("report %s has invalid divergence ID %s", DivergenceDashboardDataset, fact.SubjectID)
		}
		number, err := strconv.Atoi(numberText)
		if err != nil || number < 1 {
			return nil, fmt.Errorf("report %s has invalid divergence ID %s", DivergenceDashboardDataset, fact.SubjectID)
		}
		fields, err := decodeJSONObject(fact.Value)
		if err != nil || len(fields) != 1 || fields[0].name != "section" {
			return nil, fmt.Errorf("report %s fact %s has invalid fields", DivergenceDashboardDataset, fact.ID)
		}
		var section string
		if err := json.Unmarshal(fields[0].value, &section); err != nil {
			return nil, fmt.Errorf("report %s fact %s has invalid section", DivergenceDashboardDataset, fact.ID)
		}
		heading, _, _ := strings.Cut(section, "\n")
		title, ok := strings.CutPrefix(heading, "## "+fact.SubjectID+" ")
		if !ok || title == "" {
			return nil, fmt.Errorf("report %s fact %s heading does not match its subject", DivergenceDashboardDataset, fact.ID)
		}
		if strings.ContainsAny(title, "|\t\r\n") {
			return nil, fmt.Errorf("report %s title contains a report delimiter", DivergenceDashboardDataset)
		}
		approved := strings.Contains(section, "SCRUTINIZED:approved")
		row := divergenceReportRow{
			id: fact.SubjectID, number: number, title: title, scrutiny: "unapproved", state: VerdictOpen,
			reason: "imported divergence has no imported approval claim",
		}
		if claim, exists := claims[fact.SubjectID]; exists {
			if claim.Status != "approved" || !approved {
				return nil, fmt.Errorf("report %s claim %s does not match scrutiny marker", DivergenceDashboardDataset, claim.ID)
			}
			row.scrutiny = "approved"
			row.state = VerdictProvisional
			row.reason = "imported status approved"
			delete(claims, fact.SubjectID)
		} else if approved {
			return nil, fmt.Errorf("report %s divergence %s has an approval marker without a claim", DivergenceDashboardDataset, fact.SubjectID)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("report %s has no divergences", DivergenceDashboardDataset)
	}
	if err := rejectOrphanClaims(DivergenceDashboardDataset, claims); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(left, right divergenceReportRow) int {
		return cmp.Or(cmp.Compare(left.number, right.number), cmp.Compare(left.id, right.id))
	})
	var output bytes.Buffer
	output.WriteString("| divergence | title | imported scrutiny | proven | waived | provisional | open | contradicted | reason |\n")
	output.WriteString("|---|---|---|---:|---:|---:|---:|---:|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&output, "| %s | %s | %s | %d | %d | %d | %d | %d | %s |\n",
			row.id, row.title, row.scrutiny,
			stateColumn(row.state, VerdictProven), stateColumn(row.state, VerdictWaived),
			stateColumn(row.state, VerdictProvisional), stateColumn(row.state, VerdictOpen),
			stateColumn(row.state, VerdictContradicted), row.reason)
	}
	return output.Bytes(), nil
}
