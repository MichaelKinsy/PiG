package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/parity/closure"
	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

type inputPaths []string

func (paths *inputPaths) String() string { return strings.Join(*paths, ",") }
func (paths *inputPaths) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("input path is empty")
	}
	*paths = append(*paths, value)
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "closure:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("expected import-denominators, rebuild, status, explain, why-open, frontier, mapping-review, report, assertion-audit, evidence-readiness, execute, execute-mutation, toolchain-hash, environment-hash, coverage-audit, or verify")
	}
	switch args[0] {
	case "import-denominators":
		return runImportDenominators(ctx, args[1:], stdout, stderr)
	case "rebuild":
		return runRebuild(ctx, args[1:], stdout, stderr)
	case "status":
		return runRead(ctx, args[1:], stdout, stderr, closure.ReadStatus)
	case "verify":
		return runVerify(ctx, args[1:], stdout, stderr)
	case "explain":
		return runExplain(ctx, args[1:], stdout, stderr)
	case "why-open":
		return runRead(ctx, args[1:], stdout, stderr, closure.WhyOpen)
	case "frontier":
		return runFrontier(ctx, args[1:], stdout, stderr)
	case "mapping-review":
		return runRead(ctx, args[1:], stdout, stderr, closure.MappingReview)
	case "report":
		return runReport(ctx, args[1:], stdout, stderr)
	case "assertion-audit":
		return runAssertionAudit(ctx, args[1:], stdout, stderr)
	case "evidence-readiness":
		return runRead(ctx, args[1:], stdout, stderr, closure.EvidenceReadiness)
	case "execute":
		return runExecute(ctx, args[1:], stdout, stderr)
	case "execute-mutation":
		return runExecuteMutation(ctx, args[1:], stdout, stderr)
	case "toolchain-hash":
		hash, err := closure.CurrentToolchainHash(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, hash)
		return err
	case "environment-hash":
		hash, err := closure.CurrentEnvironmentHash([]string{})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, hash)
		return err
	case "coverage-audit":
		return runCoverageAudit(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runExecuteMutation(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("execute-mutation", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	requestID := set.String("request", "", "mutation request ID")
	outputPath := set.String("out", "", "new mutation artifact directory under the repository root")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *requestID == "" || *outputPath == "" {
		return errors.New("execute-mutation requires -request and -out")
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	run, err := closure.ExecuteMutationRequest(ctx, path, *root, *requestID, *outputPath)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\t%s\n", run.ID, run.RequestID)
	return err
}

func runExecute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("execute", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	requestID := set.String("request", "", "evidence request ID")
	outputPath := set.String("out", "", "new evidence artifact directory under the repository root")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *requestID == "" || *outputPath == "" {
		return errors.New("execute requires -request and -out")
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	evidence, err := closure.ExecuteRequest(ctx, path, *root, *requestID, *outputPath)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\t%s\t%s\n", evidence.ID, evidence.RequestID, evidence.Result)
	return err
}

func runFrontier(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("frontier", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("frontier requires one record ID")
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	output, err := closure.Frontier(ctx, path, set.Arg(0))
	if err != nil {
		return err
	}
	_, err = stdout.Write(output)
	return err
}

func runAssertionAudit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("assertion-audit", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	if err := set.Parse(args); err != nil {
		return err
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	report, err := closure.AssertionAudit(ctx, path, *root)
	if err != nil {
		return err
	}
	_, err = stdout.Write(report)
	return err
}

func runImportDenominators(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("import-denominators", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	upstreamCommit := set.String("upstream-commit", "", "exact pinned upstream commit")
	targetCommit := set.String("target-commit", "", "exact Pig commit")
	toolchainHash := set.String("toolchain-hash", "", "exact toolchain manifest hash")
	environmentHash := set.String("environment-hash", "", "typed evidence environment hash")
	var decisionPaths inputPaths
	var evidencePaths inputPaths
	var mutationPaths inputPaths
	var mutationRunPaths inputPaths
	var bundlePaths inputPaths
	set.Var(&decisionPaths, "decision", "reviewed decision JSONL; repeatable")
	set.Var(&evidencePaths, "evidence", "tracked executor evidence JSONL; repeatable")
	set.Var(&mutationPaths, "mutation", "mutation definition JSONL; repeatable")
	set.Var(&mutationRunPaths, "mutation-run", "tracked executor mutation JSONL; repeatable")
	set.Var(&bundlePaths, "bundle", "agent bundle submission candidate JSON; repeatable")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *upstreamCommit == "" || *targetCommit == "" || *toolchainHash == "" || *environmentHash == "" {
		return errors.New("import-denominators requires -upstream-commit, -target-commit, -toolchain-hash, and -environment-hash")
	}
	snapshotMaterial := *upstreamCommit + "\x00" + *targetCommit + "\x00" + *toolchainHash + "\x00" + *environmentHash
	snapshot := &closure.Snapshot{
		Kind: closure.KindSnapshot, ID: "snapshot:" + strings.TrimPrefix(closure.HashBytes([]byte(snapshotMaterial)), "sha256:"),
		UpstreamCommit: *upstreamCommit, TargetCommit: *targetCommit, ToolchainHash: *toolchainHash, EnvironmentHash: *environmentHash,
	}
	records, report, err := closure.ImportCurrentDenominators(*root, snapshot)
	if err != nil {
		return err
	}
	for _, configured := range decisionPaths {
		path, err := resolveWithinRoot(*root, configured)
		if err != nil {
			return fmt.Errorf("decision %q: %w", configured, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open decision %q: %w", configured, err)
		}
		decoded, decodeErr := closure.DecodeJSONL(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode decision %q: %w", configured, decodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close decision %q: %w", configured, closeErr)
		}
		for _, record := range decoded {
			if record.RecordKind() != closure.KindDecision {
				return fmt.Errorf("decision %q contains %s record", configured, record.RecordKind())
			}
		}
		records = append(records, decoded...)
	}
	records, err = closure.AddProviderWireBehaviors(*root, snapshot, records)
	if err != nil {
		return err
	}
	records, err = closure.AddProviderWireAssertions(*root, snapshot, records)
	if err != nil {
		return err
	}
	records, err = closure.AddProviderWireEvidenceRequests(records)
	if err != nil {
		return err
	}
	records, err = closure.AddCorrespondenceDenominator(ctx, *root, snapshot, records)
	if err != nil {
		return err
	}
	records, err = closure.AddCorrespondenceAssertions(*root, snapshot, records)
	if err != nil {
		return err
	}
	records, err = closure.AddCorrespondenceEvidenceRequests(records)
	if err != nil {
		return err
	}
	for _, configured := range mutationPaths {
		path, err := resolveWithinRoot(*root, configured)
		if err != nil {
			return fmt.Errorf("mutation %q: %w", configured, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open mutation %q: %w", configured, err)
		}
		decoded, decodeErr := closure.DecodeJSONL(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode mutation %q: %w", configured, decodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close mutation %q: %w", configured, closeErr)
		}
		for _, record := range decoded {
			if record.RecordKind() != closure.KindMutant && record.RecordKind() != closure.KindMutationRequest {
				return fmt.Errorf("mutation %q contains %s record", configured, record.RecordKind())
			}
		}
		records = append(records, decoded...)
	}
	for _, configured := range evidencePaths {
		attested, err := closure.LoadEvidenceAttestation(ctx, *root, configured)
		if err != nil {
			return fmt.Errorf("evidence %q: %w", configured, err)
		}
		records = append(records, attested...)
	}
	for _, configured := range mutationRunPaths {
		attested, err := closure.LoadMutationRun(ctx, *root, configured)
		if err != nil {
			return fmt.Errorf("mutation run %q: %w", configured, err)
		}
		records = append(records, attested...)
	}
	canonicalBundles, err := committedBundlePaths(ctx, *root, snapshot.ID)
	if err != nil {
		return err
	}
	bundlePaths = append(bundlePaths, canonicalBundles...)
	slices.Sort(bundlePaths)
	bundlePaths = slices.Compact(bundlePaths)
	for _, configured := range bundlePaths {
		path, err := resolveWithinRoot(*root, configured)
		if err != nil {
			return fmt.Errorf("bundle %q: %w", configured, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open bundle %q: %w", configured, err)
		}
		submission, decodeErr := correspondence.DecodeAgentBundleSubmission(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode bundle %q: %w", configured, decodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close bundle %q: %w", configured, closeErr)
		}
		records, err = closure.AddAgentBundleSubmission(records, submission)
		if err != nil {
			return fmt.Errorf("import bundle %q: %w", configured, err)
		}
	}
	graph, err := closure.Build(records)
	if err != nil {
		return err
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	if err := closure.RebuildStore(ctx, path, graph); err != nil {
		return err
	}
	datasets := make([]string, 0, len(report.Counts))
	for dataset := range report.Counts {
		datasets = append(datasets, dataset)
	}
	slices.Sort(datasets)
	for _, dataset := range datasets {
		if _, err := fmt.Fprintf(stdout, "%s\t%d\n", dataset, report.Counts[dataset]); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "provisional\t%d\nsource-files\t%d\nrecords\t%d\n", report.ProvisionalClaims, len(report.SourceFiles), len(records))
	return err
}

func committedBundlePaths(ctx context.Context, root, snapshotID string) ([]string, error) {
	directory := filepath.ToSlash(filepath.Join("parity", "closuredata", "proposals", strings.TrimPrefix(snapshotID, "snapshot:")))
	command := exec.CommandContext(ctx, "git", "ls-files", "-z", "--", directory)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("discover committed agent bundles: %w", err)
	}
	var paths []string
	for path := range strings.SplitSeq(string(output), "\x00") {
		if path == "" || filepath.ToSlash(filepath.Dir(path)) != directory || filepath.Ext(path) != ".json" {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("inspect committed agent bundle %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("committed agent bundle %s is not a regular file", path)
		}
		for _, args := range [][]string{{"diff", "--quiet", "HEAD", "--", path}, {"diff", "--cached", "--quiet", "HEAD", "--", path}} {
			check := exec.CommandContext(ctx, "git", args...)
			check.Dir = root
			if err := check.Run(); err != nil {
				return nil, fmt.Errorf("committed agent bundle %s differs from HEAD", path)
			}
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}

func runCoverageAudit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("coverage-audit", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	profilePath := set.String("profile", "", "Go coverage profile")
	databasePath := set.String("db", "", "closure graph used to scope mapped target ranges")
	var paths inputPaths
	set.Var(&paths, "path", "repository-relative Go path; repeatable")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *profilePath == "" {
		return errors.New("coverage-audit requires -profile")
	}
	piglet, err := resolveWithinRoot(*root, *profilePath)
	if err != nil {
		return err
	}
	file, err := os.Open(piglet)
	if err != nil {
		return fmt.Errorf("open coverage profile: %w", err)
	}
	var audit closure.CoverageAudit
	var auditErr error
	if *databasePath == "" {
		audit, auditErr = closure.AuditGoCoverage(*root, file, paths)
	} else {
		path, err := closure.SafeStorePath(*root, *databasePath)
		if err != nil {
			_ = file.Close()
			return err
		}
		scopes, err := closure.MappingCoverageScopes(ctx, path)
		if err != nil {
			_ = file.Close()
			return err
		}
		for _, configured := range paths {
			scopes = append(scopes, closure.CoverageScope{Path: configured, StartLine: 1, EndLine: int(^uint(0) >> 1)})
		}
		audit, auditErr = closure.AuditGoCoverageScopes(*root, file, scopes)
	}
	closeErr := file.Close()
	if auditErr != nil {
		return auditErr
	}
	if closeErr != nil {
		return fmt.Errorf("close coverage profile: %w", closeErr)
	}
	_, err = stdout.Write(closure.RenderCoverageAudit(audit))
	return err
}

func runRebuild(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("rebuild", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	var inputs inputPaths
	set.Var(&inputs, "input", "canonical JSONL input; repeatable")
	if err := set.Parse(args); err != nil {
		return err
	}
	if len(inputs) == 0 {
		return errors.New("rebuild requires at least one -input")
	}
	var records []closure.Record
	for _, configured := range inputs {
		path, err := resolveWithinRoot(*root, configured)
		if err != nil {
			return fmt.Errorf("input %q: %w", configured, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open input %q: %w", configured, err)
		}
		decoded, decodeErr := closure.DecodeJSONL(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode input %q: %w", configured, decodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close input %q: %w", configured, closeErr)
		}
		records = append(records, decoded...)
	}
	graph, err := closure.Build(records)
	if err != nil {
		return err
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	if err := closure.RebuildStore(ctx, path, graph); err != nil {
		return err
	}
	status, err := closure.ReadStatus(ctx, path)
	if err != nil {
		return err
	}
	_, err = stdout.Write(status)
	return err
}

func runReport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("report", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	dataset := set.String("dataset", closure.PortMapDataset, "dataset to project")
	if err := set.Parse(args); err != nil {
		return err
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	output, err := closure.ReadReport(ctx, path, *dataset)
	if err != nil {
		return err
	}
	_, err = stdout.Write(output)
	return err
}

func runRead(ctx context.Context, args []string, stdout, stderr io.Writer, read func(context.Context, string) ([]byte, error)) error {
	set := flag.NewFlagSet("read", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	if err := set.Parse(args); err != nil {
		return err
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	output, err := read(ctx, path)
	if err != nil {
		return err
	}
	_, err = stdout.Write(output)
	return err
}

func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("verify", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	if err := set.Parse(args); err != nil {
		return err
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	if err := closure.VerifyStore(ctx, path); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "closure store: verified")
	return err
}

func runExplain(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("explain", flag.ContinueOnError)
	set.SetOutput(stderr)
	root := set.String("root", ".", "repository root")
	databasePath := set.String("db", "tmp/closure/graph.db", "derived SQLite graph path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("explain requires one obligation ID")
	}
	path, err := closure.SafeStorePath(*root, *databasePath)
	if err != nil {
		return err
	}
	output, err := closure.Explain(ctx, path, set.Arg(0))
	if err != nil {
		return err
	}
	_, err = stdout.Write(output)
	return err
}

func resolveWithinRoot(root, configured string) (string, error) {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return "", err
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootAbsolute, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbsolute, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes repository root")
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	realRelative, err := filepath.Rel(rootReal, pathReal)
	if err != nil {
		return "", err
	}
	if realRelative == ".." || strings.HasPrefix(realRelative, ".."+string(filepath.Separator)) {
		return "", errors.New("path symlink escapes repository root")
	}
	return pathReal, nil
}
