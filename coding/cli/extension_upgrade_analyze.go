//go:build !pig_strip_extension_sdk_go

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// goCompile is how the build of one extension runs the go command: where, with which flags and environment, and which package it compiles.
type goCompile struct {
	Dir     string
	Command string
	Flags   []string
	Env     []string
	// ListPattern selects the packages to read and BuildPattern the one to compile.
	ListPattern, BuildPattern string
	Roots                     []string
}

// withGoCompile resolves an extension the way its build does, with the staged SDK and the generated module of a factory extension, and calls use. It compiles nothing.
func (u *extensionUpgrader) withGoCompile(ctx context.Context, config subprocess.ExtConfig, use func(goCompile) error) error {
	roots := append([]string{config.Source}, config.GoWorkspaceModules...)
	if isGoFactoryExtension(config) {
		return runtimecell.WithGoBuildModule(ctx, goExtensionOf(config), u.sdkDir(), func(module runtimecell.GoBuildModule) error {
			return use(goCompile{Dir: module.Dir, Command: module.Command, Env: module.Env, ListPattern: ".", BuildPattern: ".", Roots: roots})
		})
	}
	builder := subprocess.NewBuilderWithConfigRoot(filepath.Join(u.cacheRoot(), "ext"), u.configRoot)
	return builder.WithGoListing(config.Source, func(listing subprocess.GoListing) error {
		return use(goCompile{Dir: config.Source, Command: listing.Command, Flags: listing.Flags, Env: listing.Env, ListPattern: "./...", BuildPattern: ".", Roots: roots})
	})
}

// pig divergence (D272): the type check of an upgrade runs in a driver program that the go command builds from the staged SDK, not in this binary. analysisDriver is the package of the staged SDK that type checks an extension and plans the rewrites. This binary does not link the type checker (go/types and the importers are about 1 MiB of code that only the analysis runs): the go command builds the driver from the SDK this binary stages, with the toolchain the extension's own build uses, and runs it. The build cache keeps the driver, so only the first analysis after a Go or SDK change compiles it.
const analysisDriver = "./upgrade/driver"

// pig divergence (D272): analysisKillDelay bounds how long the cancel of an analysis waits for the driver and its go command to let go of the output pipes.
const analysisKillDelay = 5 * time.Second

// pig divergence (D272): the analysis runs only here, in the driver, and needs the staged SDK. analyzeExtension type checks the extension the way its build sees it and plans the rewrites.
func (u *extensionUpgrader) analyzeExtension(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
	// The stage lock is exclusive and the build lease below is shared, so the SDK is staged before the lease is taken.
	if err := pigsdk.EnsureSyncedLang(u.configRoot, "go"); err != nil {
		return nil, err
	}
	sdkRoot, err := pigsdk.SDKDirFor(u.configRoot, "go")
	if err != nil {
		return nil, err
	}
	var result *upgrade.Result
	err = u.withGoCompile(ctx, config, func(compile goCompile) error {
		options := upgrade.LoadOptions{Dir: compile.Dir, Roots: compile.Roots, Flags: compile.Flags, Command: compile.Command, Env: compile.Env, Patterns: []string{compile.ListPattern}}
		var err error
		result, err = runAnalysisDriver(ctx, u.configRoot, sdkRoot, compile.Command, compile.Env, options)
		return err
	})
	return result, err
}

// pig divergence (D272): runAnalysisDriver runs the analysis driver of the staged SDK at sdkRoot for options and returns its plan. The go command and its environment are the ones that list the extension, with the working directory moved to the SDK, so the driver builds as the extension does, offline, because the SDK module requires nothing.
//
// The request goes in through the driver's standard input, which stays open while the driver works: the driver cancels its own go command when it closes, and a cancelled analysis closes it. The go command that runs the driver does not pass a kill on to the driver.
func runAnalysisDriver(ctx context.Context, configRoot, sdkRoot, command string, env []string, options upgrade.LoadOptions) (*upgrade.Result, error) {
	request, err := json.Marshal(options) //portlint:allow jsonescape the driver of this binary reads the request, not a Pi-compatible file
	if err != nil {
		return nil, err
	}
	return pigsdklock.WithBuild(ctx, configRoot, func() (*upgrade.Result, error) {
		cmd := exec.CommandContext(ctx, command, "run", "-trimpath", analysisDriver)
		cmd.Dir = sdkRoot
		cmd.Env = toolchain.WorkDirEnv(sdkRoot, env)
		cmd.WaitDelay = analysisKillDelay
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("go run %s: %w", analysisDriver, err)
		}
		stopDriver := context.AfterFunc(ctx, func() { _ = stdin.Close() })
		// The request can exceed a pipe's buffer, which the driver empties once it runs, and a cold build comes first: write it beside the wait.
		written := make(chan struct{})
		go func() {
			defer close(written)
			_, _ = stdin.Write(append(request, '\n'))
		}()
		runErr := cmd.Wait()
		<-written
		stopDriver()
		if ctx.Err() != nil {
			// A cancelled analysis is cut short: its plan would lack what the driver had not read.
			return nil, ctx.Err()
		}
		// The driver's reply is upgrade.Reply of the SDK module, which the SDK release this module requires does not have yet: the JSON shape is declared here.
		var reply struct {
			Result *upgrade.Result
			Error  string
		}
		if decodeErr := json.Unmarshal(stdout.Bytes(), &reply); decodeErr != nil {
			if runErr != nil {
				return nil, fmt.Errorf("go run %s: %w: %s", analysisDriver, runErr, strings.TrimSpace(stderr.String()))
			}
			return nil, fmt.Errorf("read the analysis driver's reply: %w", decodeErr)
		}
		if reply.Error != "" {
			return nil, errors.New(reply.Error)
		}
		if reply.Result == nil {
			return nil, errors.New("the analysis driver sent no plan")
		}
		return reply.Result, nil
	})
}

// checkRewrite compiles the extension with the plan's files in place of the ones on disk, in a temporary directory, so nothing the user owns changes. It is the build the upgrade promises: the go command, the staged SDK and the module are the ones a start uses.
func (u *extensionUpgrader) checkRewrite(ctx context.Context, config subprocess.ExtConfig, result *upgrade.Result) (rewriteCheck, error) {
	var check rewriteCheck
	err := u.withGoCompile(ctx, config, func(compile goCompile) error {
		scratch, err := os.MkdirTemp("", "pig-upgrade-check-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(scratch) }()
		replace := make(map[string]string, len(result.Files))
		rewritten := make(map[string][]byte, len(result.Files))
		for i, change := range result.Files {
			copyPath := filepath.Join(scratch, strconv.Itoa(i)+".go")
			if err := os.WriteFile(copyPath, change.After, 0o600); err != nil {
				return err
			}
			replace[change.Path] = copyPath
			rewritten[change.Path] = change.After
		}
		//portlint:allow mapkeyorder the go command reads the overlay by key; its order carries no meaning
		overlay, err := json.Marshal(map[string]map[string]string{"Replace": replace}) //portlint:allow jsonescape the overlay is read by the go command, not a Pi-compatible file
		if err != nil {
			return err
		}
		overlayPath := filepath.Join(scratch, "overlay.json")
		if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
			return err
		}
		args := append([]string{"build", "-buildvcs=false", "-gcflags=-e", "-overlay=" + overlayPath, "-o", filepath.Join(scratch, "out")}, compile.Flags...)
		//nolint:gosec // G204 (class b): program is the Go toolchain command that PiG's toolchain resolver or the staged cell module names; args are fixed build flags, a scratch overlay and -o path, and -modfile from the same resolver.
		cmd := exec.CommandContext(ctx, compile.Command, append(args, compile.BuildPattern)...)
		cmd.Dir = compile.Dir
		cmd.Env = compile.Env
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		runErr := cmd.Run()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		check = classifyRewriteBuild(runErr, output.Bytes(), compile.Dir, rewritten, result)
		return nil
	})
	return check, err
}

// compileErrorPosition matches the "path:line:col: " that starts a compile error message of upgrade.Result.Errors, up to the first ":line:col: ".
var compileErrorPosition = lazyregexp.New(`^.*?:\d+:\d+: `)

// classifyRewriteBuild reads the output of the build of a rewritten extension. A compile error the plan predicted (a use it lists for the author, or a compile error the extension already had) is expected; any other is not.
func classifyRewriteBuild(runErr error, output []byte, dir string, rewritten map[string][]byte, result *upgrade.Result) rewriteCheck {
	if runErr == nil {
		return rewriteCheck{Built: true}
	}
	source := func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if data, ok := rewritten[path]; ok {
			return data, nil
		}
		return os.ReadFile(path)
	}
	known := map[string]bool{}
	for _, message := range result.Errors {
		// "path:line:col: message": the message follows the first ":line:col: ". The path may hold a colon of its own (a Windows drive), so the message does not start after the third colon.
		if at := compileErrorPosition.FindStringIndex(message); at != nil {
			known[strings.TrimSpace(message[at[1]:])] = true
		}
	}
	var check rewriteCheck
	diagnostics := upgrade.ParseDiagnostics(output)
	for _, diagnostic := range diagnostics {
		if len(upgrade.Classify([]upgrade.Diagnostic{diagnostic}, source)) > 0 || known[diagnostic.Message] {
			continue
		}
		check.Unexpected = append(check.Unexpected, fmt.Sprintf("%s:%d: %s", diagnostic.File, diagnostic.Line, diagnostic.Message)) //perf:ok runs once per unexpected compile error after a failed upgrade rebuild
	}
	if len(diagnostics) == 0 {
		tail := strings.TrimSpace(string(output))
		if len(tail) > 600 {
			tail = "..." + tail[len(tail)-600:]
		}
		check.Unexpected = append(check.Unexpected, errors.Join(runErr, errors.New(tail)).Error())
	}
	return check
}
