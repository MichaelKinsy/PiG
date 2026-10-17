package evals

// Ports packages/evals/src/cli.ts.
//
// cli.ts is a script: its top level parses process.argv and runs the comparison. Go keeps the parser as ParseEvalCli
// and the run as RunEvalCli, which takes its process inputs from EvalCli.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// DocsEvalSuffix ends the name of a documentation eval file. Pi's is ".docs.eval.ts"; PiG's suites are Go files.
const DocsEvalSuffix = ".docs.eval.go"

// EvalCliOptions are the parsed command-line options of the documentation comparison.
type EvalCliOptions struct {
	Provider       string
	Model          string
	RunsPerVariant int
	RequestedFiles []string
	DiscoveryArgs  []string
}

var (
	runnerOptions = []string{"--provider", "--model", "--runs-per-variant"}
	filterOptions = []string{"-t", "--testNamePattern"}
)

// ParseEvalCli parses the arguments after the program name. The model comes from --provider and --model, which must
// come together, or from PI_PROVIDER and PI_MODEL in environment (the process environment when omitted).
func ParseEvalCli(args []string, environment ...map[string]string) (EvalCliOptions, error) {
	lookup := environmentLookup(environment)
	var provider, model, runsText string
	var hasRuns, cliSelectedModel bool
	var requestedFiles, discoveryArgs []string

	for index := 0; index < len(args); index++ {
		argument := args[index]
		if strings.HasSuffix(argument, DocsEvalSuffix) {
			requestedFiles = append(requestedFiles, argument)
			continue
		}
		if slices.Contains(filterOptions, argument) {
			if index+1 >= len(args) || args[index+1] == "" {
				return EvalCliOptions{}, fmt.Errorf("Missing value for %s.", argument)
			}
			discoveryArgs = append(discoveryArgs, argument, args[index+1])
			index++
			continue
		}
		if strings.HasPrefix(argument, "--testNamePattern=") {
			if len(argument) == len("--testNamePattern=") {
				return EvalCliOptions{}, errors.New("Missing value for --testNamePattern.")
			}
			discoveryArgs = append(discoveryArgs, argument)
			continue
		}

		name, inlineValue, hasEquals := strings.Cut(argument, "=")
		if !slices.Contains(runnerOptions, name) {
			return EvalCliOptions{}, fmt.Errorf("Unsupported eval argument: %s", argument)
		}
		value := inlineValue
		if !hasEquals && index+1 < len(args) {
			value = args[index+1]
		}
		if value == "" || (!hasEquals && strings.HasPrefix(value, "-")) {
			return EvalCliOptions{}, fmt.Errorf("Missing value for %s.", name)
		}
		switch name {
		case "--provider":
			provider = value
		case "--model":
			model = value
		default:
			runsText, hasRuns = value, true
		}
		if name != "--runs-per-variant" {
			cliSelectedModel = true
		}
		if !hasEquals {
			index++
		}
	}

	provider, model = jsstring.Trim(provider), jsstring.Trim(model)
	if cliSelectedModel {
		if provider == "" || model == "" {
			return EvalCliOptions{}, errors.New("CLI model selection requires both --provider and --model.")
		}
	} else {
		provider, _ = lookup("PI_PROVIDER")
		model, _ = lookup("PI_MODEL")
		provider, model = jsstring.Trim(provider), jsstring.Trim(model)
		if (provider != "") != (model != "") {
			return EvalCliOptions{}, errors.New("Set both PI_PROVIDER and PI_MODEL, or neither.")
		}
	}

	if !hasRuns {
		runsText, _ = lookup("PI_EVAL_RUNS_PER_VARIANT")
	}
	runsPerVariant := 1.0
	if configuredRuns := jsstring.Trim(runsText); configuredRuns != "" {
		runsPerVariant = jsnumber.Parse(configuredRuns)
	}
	if runsPerVariant != float64(int64(runsPerVariant)) || runsPerVariant < 1 || runsPerVariant > maxSafeInteger {
		return EvalCliOptions{}, errors.New("Runs per variant must be a positive integer.")
	}
	return EvalCliOptions{Provider: provider, Model: model, RunsPerVariant: int(runsPerVariant), RequestedFiles: requestedFiles, DiscoveryArgs: discoveryArgs}, nil
}

// artifactRunId names one invocation's artifact directory by start time and a random ID.
func artifactRunId(now time.Time) string {
	return strings.ReplaceAll(now.UTC().Format("2006-01-02T15:04:05.000Z"), ":", "-") + "_" + uuid.NewString()
}

func containerPath(packageRoot, path string) (string, error) {
	absolute := path
	if !filepath.IsAbs(path) {
		absolute = filepath.Join(packageRoot, path)
	}
	packageRelative, err := filepath.Rel(packageRoot, absolute)
	if err != nil || strings.HasPrefix(packageRelative, "..") {
		return "", fmt.Errorf("Eval file must be inside %s: %s", packageRoot, path)
	}
	return filepath.ToSlash(packageRelative), nil
}

// containerPackageRoot is where the image holds the evals package.
const containerPackageRoot = "/repo/packages/evals/"

func normalizeDiscoveredFile(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, containerPackageRoot)
	if !ok {
		return "", fmt.Errorf("Discovered eval path is outside the container package: %s", path)
	}
	return rest, nil
}

func compareDiscovery(left, right []DiscoveredEvalCase) error {
	identities := func(cases []DiscoveredEvalCase) ([]string, error) {
		result := make([]string, len(cases))
		for i, evalCase := range cases {
			file, err := normalizeDiscoveredFile(evalCase.File)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal([]string{evalCase.FullName, file})
			if err != nil {
				return nil, err
			}
			if encoded, err = jsonstringify.Canonicalize(encoded); err != nil {
				return nil, err
			}
			result[i] = string(encoded)
		}
		slices.SortFunc(result, compareUTF16)
		return result, nil
	}
	leftIdentities, err := identities(left)
	if err != nil {
		return err
	}
	rightIdentities, err := identities(right)
	if err != nil {
		return err
	}
	if !slices.Equal(leftIdentities, rightIdentities) {
		return errors.New("Documentation variants discovered different eval cases.")
	}
	return nil
}

// EvalCli is one documentation comparison run: where its package and repository are, how it runs commands, and its
// process inputs.
type EvalCli struct {
	PackageRoot    string
	RepositoryRoot string
	Command        Command
	Environment    map[string]string
	Stdout         io.Writer
	Now            func() time.Time
	// DocumentationEvals lists the documentation eval files when no file is named; nil walks PackageRoot/evals.
	DocumentationEvals func() []string
}

// compactJSON is JSON.stringify(value).
func compactJSON(value any) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return jsonstringify.Canonicalize(encoded.Bytes())
}

// indentedJSON is JSON.stringify(value, null, 2) followed by a newline.
func indentedJSON(value any) ([]byte, error) {
	compact, err := compactJSON(value)
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

// compareUTF16 orders strings by UTF-16 code unit, as Array.prototype.sort does without a comparator.
func compareUTF16(left, right string) int {
	return slices.Compare(jsstring.ToUTF16(left), jsstring.ToUTF16(right))
}

// globDocsEvals lists the documentation eval files under packageRoot's evals directory.
func globDocsEvals(packageRoot string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(packageRoot, "evals"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), DocsEvalSuffix) {
			files = append(files, path)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return files, err
}

// RunEvalCli runs the documentation comparison for args and returns the process exit code: 1 when any pair is
// blocked, 0 otherwise. A configuration or infrastructure failure is returned as an error.
func (cli EvalCli) RunEvalCli(ctx context.Context, args []string) (int, error) {
	options, err := ParseEvalCli(args, cli.Environment)
	if err != nil {
		return 0, err
	}
	if options.Provider == "" || options.Model == "" {
		return 0, errors.New("Set PI_PROVIDER and PI_MODEL, or pass --provider and --model.")
	}
	modelIdentity := options.Provider + "/" + options.Model
	selected := options.RequestedFiles
	if len(selected) == 0 {
		if cli.DocumentationEvals != nil {
			selected = cli.DocumentationEvals()
		} else if selected, err = globDocsEvals(cli.PackageRoot); err != nil {
			return 0, err
		}
	}
	files := make([]string, len(selected))
	for i, file := range selected {
		if files[i], err = containerPath(cli.PackageRoot, file); err != nil {
			return 0, err
		}
	}
	slices.SortFunc(files, compareUTF16)
	if len(files) == 0 {
		return 0, errors.New("No documentation eval files were selected.")
	}
	for _, file := range files {
		if !strings.HasSuffix(file, DocsEvalSuffix) {
			return 0, fmt.Errorf("Documentation runner cannot execute non-doc eval: %s", file)
		}
	}

	artifactDirectory := filepath.Join(cli.PackageRoot, ".eval", artifactRunId(cli.Now()))
	authPath, err := RequireEvalAuthFile(options.Provider)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(artifactDirectory, 0o700); err != nil {
		return 0, err
	}
	images, err := BuildImages(ctx, cli.Command, cli.PackageRoot, cli.RepositoryRoot)
	if err != nil {
		return 0, err
	}
	if images.WithDocs.ID == images.WithoutDocs.ID {
		return 0, errors.New("Documentation variants resolved to the same image.")
	}
	docker := DockerContext{Command: cli.Command, PackageRoot: cli.PackageRoot, Images: images, ArtifactDirectory: artifactDirectory,
		AuthPath: authPath, Provider: options.Provider, Model: options.Model, RunsPerVariant: options.RunsPerVariant}

	var discoveries [2][]DiscoveredEvalCase
	for i, variant := range DocumentationVariants {
		path, err := DiscoverCases(ctx, docker, variant, files, options.DiscoveryArgs)
		if err != nil {
			return 0, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return 0, err
		}
		if discoveries[i], err = ParseDiscoveredCases(decoded); err != nil {
			return 0, err
		}
	}
	if err := compareDiscovery(discoveries[0], discoveries[1]); err != nil {
		return 0, err
	}
	cases := slices.Clone(discoveries[0])
	for i := range cases {
		if cases[i].File, err = normalizeDiscoveredFile(cases[i].File); err != nil {
			return 0, err
		}
	}
	if len(cases) == 0 {
		return 0, errors.New("No documentation eval cases matched the selection.")
	}
	tasks, err := CreateTaskPlan(cases, modelIdentity, options.RunsPerVariant)
	if err != nil {
		return 0, err
	}
	return cli.execute(ctx, docker, artifactDirectory, modelIdentity, files, cases, tasks)
}

type protocolCase struct {
	EvalSet string `json:"evalSet"`
	CaseID  string `json:"caseId"`
	File    string `json:"file"`
}

type protocol struct {
	SchemaVersion  int            `json:"schemaVersion"`
	Model          string         `json:"model"`
	RunsPerVariant int            `json:"runsPerVariant"`
	Images         BuiltImages    `json:"images"`
	Files          []string       `json:"files"`
	Cases          []protocolCase `json:"cases"`
	Tasks          []EvalTask     `json:"tasks"`
}

type protocolWithDigest struct {
	protocol
	ProtocolDigest string `json:"protocolDigest"`
}

func (cli EvalCli) execute(ctx context.Context, docker DockerContext, artifactDirectory, modelIdentity string, files []string, cases []DiscoveredEvalCase, tasks []EvalTask) (int, error) {
	runProtocol := protocol{SchemaVersion: 1, Model: modelIdentity, RunsPerVariant: docker.RunsPerVariant, Images: docker.Images, Files: files, Tasks: tasks}
	for _, evalCase := range cases {
		runProtocol.Cases = append(runProtocol.Cases, protocolCase{EvalSet: evalCase.EvalSet, CaseID: evalCase.CaseID, File: evalCase.File})
	}
	protocolText, err := compactJSON(runProtocol)
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(protocolText)
	protocolDigest := hex.EncodeToString(digest[:])
	protocolFile, err := indentedJSON(protocolWithDigest{protocol: runProtocol, ProtocolDigest: protocolDigest})
	if err != nil {
		return 0, err
	}
	expectedRuns, err := indentedJSON(tasks)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "protocol.json"), protocolFile, 0o666); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "expected-runs.json"), expectedRuns, 0o666); err != nil {
		return 0, err
	}

	observations := []EvalObservation{}
	for index, task := range tasks {
		_, _ = fmt.Fprintf(cli.Stdout, "\n[%d/%d] %s > %s | %s | %s | run %d\n", index+1, len(tasks), task.EvalSet, task.CaseID, task.Variant, task.Model, task.RunNumber)
		reportPath, err := RunTask(ctx, docker, task)
		if err != nil {
			return 0, err
		}
		observation := ErroredObservation(task)
		if reportPath != "" {
			if observation, err = ReadTaskObservation(task, reportPath, artifactDirectory); err != nil {
				return 0, err
			}
		}
		observations = append(observations, observation)
		var lines strings.Builder
		for _, item := range observations {
			line, err := compactJSON(item)
			if err != nil {
				return 0, err
			}
			lines.Write(line)
			lines.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(artifactDirectory, "observations.jsonl"), []byte(lines.String()), 0o666); err != nil {
			return 0, err
		}
	}

	expected := make([]ExpectedEvalRun, len(tasks))
	for i, task := range tasks {
		expected[i] = taskIdentity(task)
	}
	report := SummarizeEvalObservations(protocolDigest, expected, observations)
	reportText := FormatEvalComparisonReport(report)
	reportJSON, err := indentedJSON(report)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "report.json"), reportJSON, 0o666); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "report.txt"), []byte(reportText+"\n"), 0o666); err != nil {
		return 0, err
	}
	_, _ = fmt.Fprintf(cli.Stdout, "\n%s\n", reportText)
	_, _ = fmt.Fprintf(cli.Stdout, "\nArtifacts: %s\n", artifactDirectory)
	if len(report.BlockedPairs) > 0 {
		return 1, nil
	}
	return 0, nil
}
