package closure

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strings"
)

type statusRow struct {
	ID       string
	State    VerdictState
	SourceID string
	Reason   string
}

type statusReport struct {
	counts map[VerdictState]int
	rows   []statusRow
	claims []ProvisionalClaim
}

func newStatusReport(verdicts []Verdict, provisionalClaims []ProvisionalClaim) (statusReport, error) {
	counts := map[VerdictState]int{
		VerdictProven: 0, VerdictWaived: 0, VerdictProvisional: 0,
		VerdictOpen: 0, VerdictContradicted: 0,
	}
	rows := make([]statusRow, 0, len(verdicts)+len(provisionalClaims))
	seen := make(map[string]struct{}, len(verdicts)+len(provisionalClaims))
	for _, verdict := range verdicts {
		if _, known := counts[verdict.State]; !known {
			return statusReport{}, fmt.Errorf("%s has unknown verdict state %q", verdict.ObligationID, verdict.State)
		}
		if verdict.ObligationID == "" {
			return statusReport{}, fmt.Errorf("verdict has empty obligation ID")
		}
		if err := validateReportField("verdict obligation ID", verdict.ObligationID); err != nil {
			return statusReport{}, err
		}
		if err := validateReportField("verdict reason", verdict.Reason); err != nil {
			return statusReport{}, err
		}
		if _, duplicate := seen[verdict.ObligationID]; duplicate {
			return statusReport{}, fmt.Errorf("duplicate status row %s", verdict.ObligationID)
		}
		seen[verdict.ObligationID] = struct{}{}
		counts[verdict.State]++
		rows = append(rows, statusRow{ID: verdict.ObligationID, State: verdict.State, Reason: verdict.Reason})
	}
	claims := slices.Clone(provisionalClaims)
	slices.SortFunc(claims, func(left, right ProvisionalClaim) int {
		return cmp.Or(cmp.Compare(left.SubjectID, right.SubjectID), cmp.Compare(left.ID, right.ID))
	})
	for _, claim := range claims {
		if claim.ID == "" {
			return statusReport{}, fmt.Errorf("provisional claim has empty ID")
		}
		if claim.SubjectID == "" || claim.Status == "" {
			return statusReport{}, fmt.Errorf("provisional claim %s has empty subject or status", claim.ID)
		}
		fields := []struct {
			name  string
			value string
		}{
			{name: "provisional claim ID", value: claim.ID},
			{name: "provisional claim subject", value: claim.SubjectID},
			{name: "provisional claim source", value: claim.SourcePinID},
			{name: "provisional claim status", value: claim.Status},
		}
		for _, field := range fields {
			if err := validateReportField(field.name, field.value); err != nil {
				return statusReport{}, err
			}
		}
		rowID := "provisional:" + claim.ID
		if _, duplicate := seen[rowID]; duplicate {
			return statusReport{}, fmt.Errorf("duplicate status row %s", rowID)
		}
		seen[rowID] = struct{}{}
		counts[VerdictProvisional]++
		rows = append(rows, statusRow{ID: rowID, State: VerdictProvisional, SourceID: claim.SourcePinID, Reason: "imported status " + claim.Status})
	}
	slices.SortFunc(rows, func(left, right statusRow) int { return cmp.Compare(left.ID, right.ID) })
	return statusReport{counts: counts, rows: rows, claims: claims}, nil
}

// RenderStatus retains the source count and provisional-claim sections. The
// status-row projection is the generated per-obligation view; its state columns
// are derived from the rows, so imported claims cannot add credit to proven or
// waived totals.
func RenderStatus(verdicts []Verdict, provisionalClaims ...ProvisionalClaim) ([]byte, error) {
	report, err := newStatusReport(verdicts, provisionalClaims)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	for _, state := range []VerdictState{VerdictProven, VerdictWaived, VerdictProvisional, VerdictOpen, VerdictContradicted} {
		fmt.Fprintf(&output, "%s\t%d\n", state, report.counts[state])
	}
	output.WriteString("\nstate\tproven\twaived\tprovisional\topen\tcontradicted\n")
	fmt.Fprintf(&output, "count\t%d\t%d\t%d\t%d\t%d\n", report.counts[VerdictProven], report.counts[VerdictWaived], report.counts[VerdictProvisional], report.counts[VerdictOpen], report.counts[VerdictContradicted])
	output.WriteString("\nstatus-row\tid\tstate\tproven\twaived\tprovisional\topen\tcontradicted\tsource\treason\n")
	for _, row := range report.rows {
		fmt.Fprintf(&output, "status-row\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n", row.ID, row.State, stateColumn(row.State, VerdictProven), stateColumn(row.State, VerdictWaived), stateColumn(row.State, VerdictProvisional), stateColumn(row.State, VerdictOpen), stateColumn(row.State, VerdictContradicted), row.SourceID, row.Reason)
	}
	output.WriteString("\nobligation\tstate\treason\n")
	for _, row := range report.rows {
		if strings.HasPrefix(row.ID, "provisional:") {
			continue
		}
		fmt.Fprintf(&output, "%s\t%s\t%s\n", row.ID, row.State, row.Reason)
	}
	if len(report.claims) > 0 {
		output.WriteString("\nprovisional-claim\tdenominator-status\n")
		for _, claim := range report.claims {
			fmt.Fprintf(&output, "%s\t%s\n", claim.SubjectID, claim.Status)
		}
	}
	return output.Bytes(), nil
}

func validateReportField(name, value string) error {
	if strings.ContainsAny(value, "\t\r\n") {
		return fmt.Errorf("%s contains a report delimiter", name)
	}
	return nil
}

func stateColumn(actual, column VerdictState) int {
	if actual == column {
		return 1
	}
	return 0
}
