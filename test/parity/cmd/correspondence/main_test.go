package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/test/parity/correspondence"
	"github.com/MichaelKinsy/PiG/test/parity/internal/gitsnapshot"
	"github.com/MichaelKinsy/PiG/test/parity/knowngaps"
)

func TestCompareCurrentPin(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err = run(context.Background(), []string{
		"compare", "-root", root, "-upstream-version", coding.UpstreamVersion, "-target-worktree", "-node", node,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v, stderr=%s", err, stderr.String())
	}
	var report correspondence.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	source, err := correspondence.ExtractTypeScript(t.Context(), node,
		filepath.Join(root, "test/parity/interface-extractor/src/extract-correspondence.mjs"),
		filepath.Join(root, ".upstream/current"), coding.UpstreamVersion)
	if err != nil {
		t.Fatal(err)
	}
	rules := correspondence.CompactionSettingsRules()
	wantMappings := len(source.Constants) + len(rules.CallContracts)
	for _, table := range source.Tables {
		wantMappings += len(table.Items) + len(rules.AdditiveTableItems[rules.TableTargets[table.ID]])
	}
	known := knownCorrespondenceGaps(t, root)
	// A Pi table item the Pig table lacks yields a missing-table-item finding
	// and no mapping, so each listed one leaves the mapping denominator.
	for key := range known {
		if strings.HasPrefix(key, "finding:table-item:missing:") {
			wantMappings--
		}
	}
	if len(report.Mappings) != wantMappings || len(report.Findings) != len(known) {
		t.Fatalf("report mappings=%d findings=%d", len(report.Mappings), len(report.Findings))
	}
}

func TestPacketCurrentPin(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	var stdout, stderr bytes.Buffer
	err = run(context.Background(), []string{
		"packet", "-root", root, "-upstream-version", coding.UpstreamVersion, "-target-commit", commit, "-node", node,
	}, &stdout, &stderr)
	if packetBlockedByKnownGaps(t, root, err, stderr.String()) {
		return
	}
	var packet correspondence.AlignmentWorkPacket
	if err := json.Unmarshal(stdout.Bytes(), &packet); err != nil {
		t.Fatal(err)
	}
	if err := packet.Validate(); err != nil {
		t.Fatal(err)
	}
	// The reviewed current settings inventory has 34 rows (independently
	// counted from settings-selector.ts; see TestCompareCurrentPin above),
	// and closing the correspondence gaps places every row in the work
	// packet. Reproduce with:
	//   go run ./test/parity/cmd/correspondence packet -root . -upstream-version 0.99.2 -target-commit <commit> -node "$(command -v node)"
	if len(packet.Questions) != 34 || len(packet.Functions) != 10 {
		t.Fatalf("packet settings/functions = %d/%d, want 34/10", len(packet.Questions), len(packet.Functions))
	}
	foundPersistence := false
	for _, question := range packet.Functions {
		if question.ID == "alignment:settings-manager:persistScopedSettings" {
			foundPersistence = slices.Equal(question.RequiredAnalyses, []string{"error-surfacing", "external-edit-preservation", "field-granularity", "lock-serialization"})
		}
	}
	if !foundPersistence {
		t.Fatalf("packet lacks persistence alignment contract: %#v", packet.Functions)
	}
}

func TestCompareRequiresExactIdentities(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"compare"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "requires -upstream-version") {
		t.Fatalf("run() error = %v, want upstream identity requirement", err)
	}
	err = run(context.Background(), []string{"compare", "-upstream-version", coding.UpstreamVersion}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "requires exactly one of -target-commit or -target-worktree") {
		t.Fatalf("run() error = %v, want target identity requirement", err)
	}
	err = run(context.Background(), []string{"compare", "-upstream-version", coding.UpstreamVersion, "-target-commit", "abc", "-target-worktree"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "requires exactly one of -target-commit or -target-worktree") {
		t.Fatalf("run() error = %v, want exclusive target identity requirement", err)
	}
}

func TestWorkPacketCurrentPinKeepsAgentReadOnly(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	var stdout, stderr bytes.Buffer
	err = run(t.Context(), []string{
		"work-packet", "-root", root, "-upstream-version", coding.UpstreamVersion, "-target-commit", commit, "-role", correspondence.AdversaryRole, "-snapshot-id", "snapshot:test",
	}, &stdout, &stderr)
	if packetBlockedByKnownGaps(t, root, err, stderr.String()) {
		return
	}
	packet, err := correspondence.DecodeAgentWorkPacket(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	wantRuntimeCandidates := []string{
		"alignment:settings:fullscreen-scrollbar", "alignment:settings:mermaid-rendering", "alignment:settings:model-thinking",
	}
	var runtimeCandidates []string
	for _, edge := range packet.UnresolvedEdges {
		if strings.HasPrefix(edge, "alignment:settings:") {
			runtimeCandidates = append(runtimeCandidates, edge)
		}
	}
	// Including the full 34-row settings inventory (see TestCompareCurrentPin)
	// adds three alignment questions and their five obligations to the
	// previous reviewed packet, plus the fullscreen-wheel-scroll-lines row:
	// one question and the five obligations every settings row carries
	// (current-state, dispatch, persistence, runtime-effects, value-domain).
	// Reproduce with:
	//   go run ./test/parity/cmd/correspondence work-packet -root . -upstream-version 0.99.2 -target-commit <commit> -role adversary -snapshot-id snapshot:test
	if packet.Role != correspondence.AdversaryRole || packet.SnapshotID != "snapshot:test" || len(packet.Questions) != 44 || len(packet.ObligationIDs) != 213 || len(packet.UnresolvedEdges) != 11 || !slices.Equal(runtimeCandidates, wantRuntimeCandidates) || len(packet.WritePaths) != 0 {
		t.Fatalf("work packet = role %s snapshot %s questions %d obligations %d unresolved %v writes %d", packet.Role, packet.SnapshotID, len(packet.Questions), len(packet.ObligationIDs), packet.UnresolvedEdges, len(packet.WritePaths))
	}
}

func TestSubmitBundleStoresCanonicalProposal(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	var packetOutput, stderr bytes.Buffer
	err = run(t.Context(), []string{
		"work-packet", "-root", root, "-upstream-version", coding.UpstreamVersion, "-target-commit", commit,
		"-role", correspondence.AdversaryRole, "-snapshot-id", "snapshot:test",
	}, &packetOutput, &stderr)
	if packetBlockedByKnownGaps(t, root, err, stderr.String()) {
		return
	}
	packet, err := correspondence.DecodeAgentWorkPacket(bytes.NewReader(packetOutput.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	question := packet.Questions[0]
	bundle, err := correspondence.BindAdversaryBundle(packet, []correspondence.AdversaryFinding{{
		QuestionID: question.ID, Fault: "uncovered-case", Rationale: "boundary transition is not exercised",
		Analyses: []string{question.RequiredAnalyses[0]}, Citations: question.Citations,
	}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := correspondence.BindAgentBundleSubmission(packet, bundle)
	if err != nil {
		t.Fatal(err)
	}
	temporaryRoot := t.TempDir()
	packetPath := filepath.Join(temporaryRoot, "packet.json")
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packetPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(temporaryRoot, "bundle.json")
	encoded, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	stderr.Reset()
	if err := run(t.Context(), []string{
		"submit-bundle", "-root", temporaryRoot, "-packet", "packet.json", "-bundle", "bundle.json", "-out", "proposals",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("submit-bundle: %v stderr=%s", err, stderr.String())
	}
	fields := strings.Fields(stdout.String())
	if len(fields) != 2 || fields[0] != submission.ID || !strings.HasPrefix(fields[1], "proposals/test/") {
		t.Fatalf("submit output = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(temporaryRoot, filepath.FromSlash(fields[1]))); err != nil {
		t.Fatal(err)
	}
}

func repositorySnapshotCommit(t *testing.T, root string) string {
	t.Helper()
	commit, err := gitsnapshot.Create(t.Context(), root, filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func knownCorrespondenceGaps(t *testing.T, root string) map[string]knowngaps.Entry {
	t.Helper()
	ledger, err := knowngaps.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Scope(knownGapScope)
}

// packetBlockedByKnownGaps reports whether listed correspondence gaps block the
// alignment packet, which the builder refuses while any finding exists.
func packetBlockedByKnownGaps(t *testing.T, root string, err error, stderr string) bool {
	t.Helper()
	blocked, problem := knowngaps.Blocked(root, knownGapScope, err, func(findings int) string {
		return fmt.Sprintf("cannot build alignment packet with %d correspondence findings", findings)
	})
	if problem != nil {
		t.Fatalf("%v; stderr=%s", problem, stderr)
	}
	if blocked {
		t.Log("listed correspondence gaps block the alignment packet")
	}
	return blocked
}

// A signal cancels main's context. run must still remove the worktree snapshot directory it made under the temporary directory, as it does on success and on every other error.
func TestWorktreeSnapshotDirectoryRemovedWhenCancelled(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, scratch)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout, stderr bytes.Buffer
	err = run(ctx, []string{"compare", "-root", root, "-upstream-version", coding.UpstreamVersion, "-target-worktree"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run succeeded with a cancelled context")
	}
	left, readErr := os.ReadDir(scratch)
	if readErr != nil || len(left) != 0 {
		t.Fatalf("snapshot scratch left behind: %v, %v", left, readErr)
	}
}
