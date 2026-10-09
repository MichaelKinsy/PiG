// Command pig-evals runs PiG's documentation evals, the Go port of Pi's packages/evals.
//
//	pig-evals docs [--provider P --model M] [--runs-per-variant N] [-t PATTERN] [FILE...]
//
// builds the without_docs and with_docs images, runs every planned case in a fresh container and writes the paired
// comparison under internal/evals/.eval (src/cli.ts). Inside an image the same binary is the entry point
// (`pig-evals entrypoint`, docker/entrypoint.ts) and runs the suites.
//
//	pig-evals host [-t PATTERN] [FILE...]
//
// runs the host evals on this machine (`vitest run --project host`) and writes vitest.json to the current directory.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"time"

	"github.com/MichaelKinsy/PiG/internal/evals"
	"github.com/MichaelKinsy/PiG/internal/evals/evalsuites"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code, err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("usage: pig-evals docs|host|entrypoint [arguments]")
	}
	switch args[0] {
	case "docs":
		return docs(ctx, args[1:])
	case "host":
		return host(ctx, args[1:])
	case "entrypoint":
		return entrypoint(ctx, args[1:])
	}
	return 2, fmt.Errorf("unknown command %q", args[0])
}

// packageRoot is the evals package directory: the module root's internal/evals.
func packageRoot() (string, string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return filepath.Join(directory, "internal", "evals"), directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", "", fmt.Errorf("no go.mod above the working directory")
		}
		directory = parent
	}
}

func docs(ctx context.Context, args []string) (int, error) {
	root, repository, err := packageRoot()
	if err != nil {
		return 0, err
	}
	environment := map[string]string{}
	for _, name := range []string{"PI_PROVIDER", "PI_MODEL", "PI_EVAL_RUNS_PER_VARIANT"} {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}
	cli := evals.EvalCli{
		PackageRoot: root, RepositoryRoot: repository, Command: evals.ExecCommand{Dir: root}, Environment: environment, Stdout: os.Stdout,
		Now: time.Now,
		DocumentationEvals: func() []string {
			var files []string
			for _, suite := range evalsuites.All() {
				if suite.Project() == evals.EvalProjectDocs {
					files = append(files, suite.File)
				}
			}
			return files
		},
	}
	return cli.RunEvalCli(ctx, args)
}

func host(ctx context.Context, args []string) (int, error) {
	selection := evals.Selection{Project: evals.EvalProjectHost}
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "-t", "--testNamePattern":
			index++
			if index >= len(args) {
				return 2, fmt.Errorf("Missing value for %s.", argument)
			}
			pattern, err := regexp.Compile(args[index])
			if err != nil {
				return 2, err
			}
			selection.NamePattern = pattern
		default:
			selection.Files = append(selection.Files, argument)
		}
	}
	runner := evals.Runner{Suites: evalsuites.All(), PackageRoot: "evals"}
	report := runner.Run(ctx, selection)
	if err := evals.WriteVitestReport("vitest.json", report); err != nil {
		return 0, err
	}
	fmt.Printf("%d passed, %d failed, %d total\n", report.NumPassedTests, report.NumFailedTests, report.NumTotalTests)
	if !report.Success {
		return 1, nil
	}
	return 0, nil
}
