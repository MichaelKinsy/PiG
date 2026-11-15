// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// submissions.ts admitSubmission stores `onAbort` only when `draft.onAbort === "keep"`: any other value of the submit
// event's field, and a field that is not a string, is withdraw, the default.
func TestSubmissionDraftReadsOnAbortKeepOnly(t *testing.T) {
	for _, tc := range []struct{ event, want string }{
		{`{"type":"input","content":"x"}`, ""},
		{`{"type":"input","content":"x","onAbort":"keep"}`, "keep"},
		{`{"type":"input","content":"x","onAbort":"withdraw"}`, ""},
		{`{"type":"input","content":"x","onAbort":"later"}`, ""},
		{`{"type":"input","content":"x","onAbort":null}`, ""},
		{`{"type":"input","content":"x","onAbort":1}`, ""},
	} {
		parsed, err := jv.ParseString(tc.event)
		if err != nil {
			t.Fatal(err)
		}
		if got := submissionDraft(parsed.(*jv.Object)).OnAbort; got != tc.want {
			t.Errorf("%s: OnAbort = %q, want %q", tc.event, got, tc.want)
		}
	}
}
