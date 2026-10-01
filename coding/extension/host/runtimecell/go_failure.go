package runtimecell

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

type goFailureKind int

const (
	// goFailureRetryable is not a property of the inputs: module resolution,
	// network and disk failures are reported but never recorded.
	goFailureRetryable goFailureKind = iota
	// goFailureInputs is a compiler diagnostic, which the same sources and SDK
	// produce again.
	goFailureInputs
	// goFailureToolchain is a compiler that does not belong to its go command,
	// which every build with that toolchain reproduces.
	goFailureToolchain
)

// classifyGoFailure reduces a failed `go build` to a BuildFailure and reports
// what kind of failure it is, which decides whether it is recorded.
func classifyGoFailure(tc toolchain.GoToolchain, output []byte) (failure *BuildFailure, kind goFailureKind) {
	if line, ok := tc.ReleaseMismatch(output); ok {
		return &BuildFailure{Summary: line, Cause: line}, goFailureToolchain
	}
	if hasGoCompilerDiagnostic(output) {
		sawHeader := false
		for line := range bytes.SplitSeq(output, []byte{'\n'}) {
			if bytes.HasPrefix(line, []byte("# ")) {
				sawHeader = true
				continue
			}
			if !sawHeader {
				continue
			}
			if message, located, ok := diagnosticMessage(line); ok {
				return &BuildFailure{Summary: oneLine("go build: " + located), Cause: oneLine("go build: " + message)}, goFailureInputs
			}
		}
	}
	summary := "go build failed"
	var last string
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		last = line
		if strings.HasPrefix(line, "go: ") {
			summary = "go build: " + line
			return &BuildFailure{Summary: oneLine(summary), Cause: oneLine(summary)}, goFailureRetryable
		}
	}
	if last != "" {
		summary = "go build: " + last
	}
	return &BuildFailure{Summary: oneLine(summary), Cause: oneLine(summary)}, goFailureRetryable
}

// GoBuildFailure reports a failed `go build` run with tc in dir: it classifies the output,
// writes the complete output to a log under cacheRoot, and remembers a broken
// toolchain for this process. recordable reports whether the same inputs fail
// again, so the caller may record the failure against them.
func GoBuildFailure(cacheRoot, logKey, dir string, tc toolchain.GoToolchain, buildErr error, output, generatedMod []byte) (failure *BuildFailure, recordable bool) {
	failure, kind := classifyGoFailure(tc, output)
	failure.Log = writeBuildLog(cacheRoot, logKey, goBuildLogContent(tc, dir, buildErr, output, generatedMod))
	if kind == goFailureToolchain {
		markBrokenToolchain(tc, failure)
	}
	return failure, kind != goFailureRetryable
}

// KnownGoToolchainFailure returns the recorded release mismatch of tc, if a
// build in this process already met it.
func KnownGoToolchainFailure(tc toolchain.GoToolchain) (*BuildFailure, bool) {
	return brokenToolchain(tc)
}

// goBuildLogContent is the complete record kept for a failed build.
func goBuildLogContent(tc toolchain.GoToolchain, dir string, buildErr error, output, generatedMod []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "time: %s\ncommand: %s\nGOROOT: %s\ndirectory: %s\nerror: %v\n\n--- go build output ---\n", time.Now().UTC().Format(time.RFC3339), tc.Command, tc.Root, dir, buildErr)
	b.Write(output)
	if len(generatedMod) > 0 {
		b.WriteString("\n--- generated go.mod ---\n")
		b.Write(generatedMod)
	}
	return b.Bytes()
}

// goCompilerRevision identifies the compiler binary of tc, so a reinstall that
// keeps the release name still changes what a build depends on.
func goCompilerRevision(tc toolchain.GoToolchain) string {
	if tc.Root == "" {
		return ""
	}
	revision, _ := toolchainFileRevision(filepath.Join(tc.Root, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, exeNameForRuntime("compile")))
	return revision
}

type brokenToolchainKey struct {
	toolchain toolchain.GoToolchain
	compiler  string
}

// brokenToolchains remembers, for this process, the toolchains whose compiler
// does not match their go command. Every cell of a load pass would otherwise run
// go, and fail on the first standard-library package, once per cell. Replacing
// the compiler changes the key, so a repaired installation is tried again.
var brokenToolchains sync.Map

func brokenToolchain(tc toolchain.GoToolchain) (*BuildFailure, bool) {
	value, ok := brokenToolchains.Load(brokenToolchainKey{tc, goCompilerRevision(tc)})
	if !ok {
		return nil, false
	}
	return value.(*BuildFailure), true
}

func markBrokenToolchain(tc toolchain.GoToolchain, failure *BuildFailure) {
	brokenToolchains.Store(brokenToolchainKey{tc, goCompilerRevision(tc)}, failure)
}
