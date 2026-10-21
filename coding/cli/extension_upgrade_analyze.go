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

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
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

// analyzeExtension type checks the extension the way its build sees it and plans the rewrites.
func (u *extensionUpgrader) analyzeExtension(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
	var result *upgrade.Result
	err := u.withGoCompile(ctx, config, func(compile goCompile) error {
		options := upgrade.LoadOptions{Dir: compile.Dir, Roots: compile.Roots, Flags: compile.Flags, Command: compile.Command, Env: compile.Env, Patterns: []string{compile.ListPattern}}
		session, err := upgrade.Open(ctx, options)
		if err != nil {
			return err
		}
		result, err = session.Plan()
		return err
	})
	return result, err
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
