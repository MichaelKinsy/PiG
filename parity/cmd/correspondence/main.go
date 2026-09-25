package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
	"github.com/MichaelKinsy/PiG/parity/internal/gitsnapshot"
	"github.com/MichaelKinsy/PiG/parity/knowngaps"
)

func submitBundle(root, packetInput, bundleInput, outputDirectory string, stdout io.Writer) error {
	result, err := correspondence.SubmitAgentBundle(root, packetInput, bundleInput, outputDirectory)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\t%s\n", result.ID, result.Path)
	return err
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "compare" && args[0] != "packet" && args[0] != "work-packet" && args[0] != "submit-bundle") {
		return errors.New("expected compare, packet, work-packet, or submit-bundle")
	}
	command := args[0]
	flags := flag.NewFlagSet("correspondence "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "Pig repository root")
	upstreamVersion := flags.String("upstream-version", "", "exact upstream version")
	targetCommit := flags.String("target-commit", "", "exact Pig commit")
	targetWorktree := flags.Bool("target-worktree", false, "snapshot the current Pig working tree without changing HEAD or the repository index")
	node := flags.String("node", "", "Node executable")
	role := flags.String("role", "", "agent role for work-packet")
	snapshotID := flags.String("snapshot-id", "", "closure snapshot identity for work-packet")
	packetPath := flags.String("packet", "", "agent work packet JSON for submit-bundle")
	bundlePath := flags.String("bundle", "", "agent output bundle JSON for submit-bundle")
	outputDirectory := flags.String("out", "parity/closuredata/proposals", "canonical submission directory under the repository root")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s accepts no positional arguments", command)
	}
	if command != "submit-bundle" && *upstreamVersion == "" {
		return fmt.Errorf("%s requires -upstream-version", command)
	}
	if command != "submit-bundle" && ((*targetCommit == "" && !*targetWorktree) || (*targetCommit != "" && *targetWorktree)) {
		return fmt.Errorf("%s requires exactly one of -target-commit or -target-worktree", command)
	}
	if command == "work-packet" && (*role == "" || *snapshotID == "") {
		return errors.New("work-packet requires -role and -snapshot-id")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if command == "submit-bundle" {
		if *packetPath == "" || *bundlePath == "" {
			return errors.New("submit-bundle requires -packet and -bundle")
		}
		return submitBundle(absoluteRoot, *packetPath, *bundlePath, *outputDirectory, stdout)
	}
	if *targetWorktree {
		snapshotDirectory, err := os.MkdirTemp("", "pig-correspondence-snapshot-*")
		if err != nil {
			return fmt.Errorf("create correspondence snapshot directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(snapshotDirectory) }()
		commit, err := gitsnapshot.Create(ctx, absoluteRoot, filepath.Join(snapshotDirectory, "index"))
		if err != nil {
			return fmt.Errorf("snapshot correspondence target: %w", err)
		}
		*targetCommit = commit
	}
	nodePath := *node
	if nodePath == "" {
		nodePath, err = exec.LookPath("node")
		if err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	source, err := correspondence.ExtractTypeScript(
		ctx,
		nodePath,
		filepath.Join(absoluteRoot, "parity/interface-extractor/src/extract-correspondence.mjs"),
		filepath.Join(absoluteRoot, ".upstream/current"),
		*upstreamVersion,
	)
	if err != nil {
		return err
	}
	target, err := correspondence.ExtractGo(ctx, absoluteRoot, *targetCommit)
	if err != nil {
		return err
	}
	report, err := correspondence.Compare(source, target, correspondence.CompactionSettingsRules())
	if err != nil {
		return err
	}
	if command == "compare" {
		if err := encoder.Encode(report); err != nil {
			return err
		}
		return checkFindings(absoluteRoot, report, stderr)
	}
	if err := checkFindings(absoluteRoot, report, stderr); err != nil {
		return err
	}
	packet, err := correspondence.BuildSettingsAlignmentPacket(source, target, report)
	if err != nil {
		return err
	}
	if command == "packet" {
		return encoder.Encode(packet)
	}
	mapped := make(map[string]struct{}, len(report.Mappings))
	for _, mapping := range report.Mappings {
		mapped[mapping.SourceID] = struct{}{}
	}
	unresolved, err := correspondence.RuntimeEffectCandidates(packet)
	if err != nil {
		return err
	}
	for _, question := range packet.Functions {
		if _, ok := mapped[question.Source.ID]; !ok {
			unresolved = append(unresolved, question.ID)
		}
	}
	scope, err := correspondence.NewAgentWorkScope(packet, *snapshotID, []string{}, []string{}, []string{})
	if err != nil {
		return err
	}
	workPacket, err := correspondence.BuildAgentWorkPacket(packet, *role, unresolved, scope)
	if err != nil {
		return err
	}
	return encoder.Encode(workPacket)
}

// checkFindings tolerates exactly the findings listed for the correspondence
// scope in parity/known-gaps.toml. Any other finding fails, and so does a
// listed finding that the comparison no longer reports.
func checkFindings(root string, report *correspondence.Report, stderr io.Writer) error {
	ledger, err := knowngaps.Load(root)
	if err != nil {
		return err
	}
	known := ledger.Scope(knownGapScope)
	observed := make(map[string]struct{}, len(report.Findings))
	var unknown []string
	for _, finding := range report.Findings {
		observed[finding.ID] = struct{}{}
		if entry, ok := known[finding.ID]; ok {
			_, _ = fmt.Fprintf(stderr, "known gap [%s] (%s): %s\n", finding.ID, entry.Tracking, finding.Detail)
			continue
		}
		unknown = append(unknown, finding.ID)
	}
	if len(unknown) > 0 {
		return fmt.Errorf("correspondence comparison found %d gap(s) not listed in %s: %s", len(unknown), knowngaps.Path, strings.Join(unknown, ", "))
	}
	if stale := ledger.Stale(knownGapScope, observed); len(stale) > 0 {
		return fmt.Errorf("%s lists correspondence gaps that are now closed; remove them: %s", knowngaps.Path, strings.Join(stale, ", "))
	}
	return nil
}

// knownGapScope selects this gate's rows in parity/known-gaps.toml.
const knownGapScope = "correspondence"
