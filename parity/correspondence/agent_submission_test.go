package correspondence

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestAgentBundleSubmissionIsCanonicalAndIdempotent(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildAgentWorkPacket(alignment, AdversaryRole, []string{alignment.Questions[0].ID}, scope)
	if err != nil {
		t.Fatal(err)
	}
	question := packet.Questions[0]
	bundle, err := BindAdversaryBundle(packet, []AdversaryFinding{{
		QuestionID: question.ID,
		Fault:      "uncovered-case",
		Rationale:  "the current test omits the false-value transition",
		Analyses:   []string{question.RequiredAnalyses[0]},
		Citations:  question.Citations,
	}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := BindAgentBundleSubmission(packet, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(submission.ID, "submission:") {
		t.Fatalf("submission ID = %s", submission.ID)
	}
	encoded, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAgentBundleSubmission(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != submission.ID {
		t.Fatalf("decoded ID = %s, want %s", decoded.ID, submission.ID)
	}

	root := t.TempDir()
	first, err := WriteAgentBundleSubmission(root, "proposals", submission)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteAgentBundleSubmission(root, "proposals", submission)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || filepath.Base(filepath.Dir(filepath.Dir(first))) != "proposals" || filepath.Base(filepath.Dir(first)) != "test" {
		t.Fatalf("submission paths = %q, %q", first, second)
	}
	stored, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(stored, []byte("\n")) {
		t.Fatalf("stored submission is not canonical JSONL: %q", stored)
	}

	if _, err := WriteAgentBundleSubmission(root, "../escape", submission); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("escaping output error = %v", err)
	}

	outside := t.TempDir()
	symlinkRoot := t.TempDir()
	testenv.Symlink(t, outside, filepath.Join(symlinkRoot, "proposals"))
	if _, err := WriteAgentBundleSubmission(symlinkRoot, "proposals", submission); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked output error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "test")); !os.IsNotExist(err) {
		t.Fatalf("symlinked output created outside root: %v", err)
	}

	submission.Adversary.Findings[0].Rationale = "changed after binding"
	if err := submission.Validate(); err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("mutated submission error = %v, want content identity rejection", err)
	}
}

func TestAgentBundleSubmissionRejectsUnknownFieldsAndEscapingOutput(t *testing.T) {
	if _, err := DecodeAlignmentWorkPacket(strings.NewReader(strings.Repeat(" ", (4<<20)+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized alignment packet error = %v", err)
	}
	if _, err := DecodeAgentWorkPacket(strings.NewReader(strings.Repeat(" ", (4<<20)+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized work packet error = %v", err)
	}
	if _, err := DecodeAgentBundleSubmission(strings.NewReader(`{"id":"submission:bad","role":"adversary","packet":{},"adversary":{},"unknown":true}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
	if _, err := WriteAgentBundleSubmission(t.TempDir(), "proposals", nil); err == nil || !strings.Contains(err.Error(), "incomplete identity") {
		t.Fatalf("nil submission error = %v", err)
	}
}
