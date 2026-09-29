package closure

import (
	"strings"
	"testing"
)

func TestRenderStatusProjectsSeparateStateColumns(t *testing.T) {
	have, err := RenderStatus(
		[]Verdict{
			{ObligationID: "obligation:open", State: VerdictOpen, Reason: "needs evidence"},
			{ObligationID: "obligation:proven", State: VerdictProven, Reason: "complete"},
			{ObligationID: "obligation:waived", State: VerdictWaived, Reason: "reviewed decision"},
		},
		ProvisionalClaim{ID: "provisional:denominator", SubjectID: "denominator-row", SourcePinID: "pin:denominator", Status: "✅"},
	)
	if err != nil {
		t.Fatalf("RenderStatus(): %v", err)
	}
	want := `proven	1
waived	1
provisional	1
open	1
contradicted	0

state	proven	waived	provisional	open	contradicted
count	1	1	1	1	0

status-row	id	state	proven	waived	provisional	open	contradicted	source	reason
status-row	obligation:open	open	0	0	0	1	0		needs evidence
status-row	obligation:proven	proven	1	0	0	0	0		complete
status-row	obligation:waived	waived	0	1	0	0	0		reviewed decision
status-row	provisional:provisional:denominator	provisional	0	0	1	0	0	pin:denominator	imported status ✅

obligation	state	reason
obligation:open	open	needs evidence
obligation:proven	proven	complete
obligation:waived	waived	reviewed decision

provisional-claim	denominator-status
denominator-row	✅
`
	if string(have) != want {
		t.Fatalf("RenderStatus() =\n%s\nwant\n%s", have, want)
	}
	if strings.Contains(string(have), "denominator-row\tproven") || strings.Contains(string(have), "denominator-row\twaived") {
		t.Fatalf("imported claim received non-provisional credit:\n%s", have)
	}
}

func TestRenderStatusRejectsDuplicateAndUnknownRows(t *testing.T) {
	if _, err := RenderStatus([]Verdict{
		{ObligationID: "obligation:test", State: VerdictState("unknown")},
	}); err == nil || !strings.Contains(err.Error(), "unknown verdict state") {
		t.Fatalf("unknown state error = %v", err)
	}
	if _, err := RenderStatus([]Verdict{
		{ObligationID: "obligation:test", State: VerdictOpen},
		{ObligationID: "obligation:test", State: VerdictOpen},
	}); err == nil || !strings.Contains(err.Error(), "duplicate status row") {
		t.Fatalf("duplicate row error = %v", err)
	}
	if _, err := RenderStatus([]Verdict{
		{ObligationID: "obligation:test", State: VerdictOpen},
	}, ProvisionalClaim{ID: "provisional:test", SubjectID: "denominator\nrow", SourcePinID: "pin:denominator", Status: "✅"}); err == nil || !strings.Contains(err.Error(), "report delimiter") {
		t.Fatalf("delimiter error = %v", err)
	}
}
