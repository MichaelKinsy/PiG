// tools/bash_executor.go: user bash execution (!cmd, RPC bash, SDK
// ExecuteBash).
//
// Upstream reference:
//
//	.upstream/current/packages/coding-agent/src/core/bash-executor.ts
//	executeBashWithOperations.
//
// The LLM bash tool does not use this path: like upstream it feeds raw output
// to an OutputAccumulator (shell_tool.go). User bash sanitizes each decoded
// chunk for display and storage.
package tools

import (
	"context"
	"strings"
	"sync/atomic"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/outputfiles"
	"github.com/MichaelKinsy/PiG/internal/text"
	"github.com/MichaelKinsy/PiG/internal/utf8stream"
)

// BashResult mirrors upstream BashResult (bash-executor.ts).
type BashResult struct {
	// Output is combined stdout+stderr after sanitize + truncate.
	Output string
	// ExitCode is the process exit code, or nil if killed/cancelled.
	ExitCode *int
	// Cancelled is true when the context was cancelled mid-run.
	Cancelled bool
	// Truncated is true when Output is shorter than the full process output.
	Truncated bool
	// FullOutputPath, when non-empty, is a temp-file path holding the
	// untruncated output. Caller is responsible for deletion.
	FullOutputPath string
	// FullOutputError explains why the full output could not be saved when truncated.
	FullOutputError string
}

// BashExecOptions mirrors upstream BashExecutorOptions; cancellation comes
// from the context.
type BashExecOptions struct {
	// OnChunk receives each sanitized chunk as it arrives. Nil = disabled.
	OnChunk func(chunk string)
	// BinDir is prepended to PATH (upstream getShellEnv's getBinDir).
	BinDir string
}

// ExecuteBash runs command under shell in cwd through local shell operations.
// ctx cancellation kills the process group.
func ExecuteBash(ctx context.Context, command, cwd string, shell ShellConfig, opts BashExecOptions) (BashResult, error) {
	ops := &LocalShellOperations{ShellName: "bash", BinDir: opts.BinDir, ResolveShell: func() (ShellConfig, error) { return shell, nil }}
	return ExecuteBashWithOperations(ctx, command, cwd, ops, opts)
}

// ExecuteBashWithOperations mirrors upstream executeBashWithOperations: each
// chunk is decoded with a streaming UTF-8 decoder, ANSI-stripped, sanitized,
// and stripped of carriage returns; a rolling window of chunks is kept, and a
// temp file receives the sanitized text once the output passes the byte
// limit. A cancelled run returns its output with Cancelled set; any other
// operations error is returned.
//
// A clean chunk costs one string, the decoded text it keeps: the strip and
// sanitize steps return text they do not change as it is, and the temp file
// is written through a buffer rather than a write per chunk.
func ExecuteBashWithOperations(ctx context.Context, command, cwd string, operations BashOperations, opts BashExecOptions) (BashResult, error) {
	const maxOutputBytes = DefaultMaxBytesUpstream * 2
	var (
		outputChunks []string
		outputBytes  int
		totalBytes   int
		tempFile     *outputfiles.Stream
		// discardedOutput is set once the rolling window dropped a chunk, so the
		// result is truncated even if the retained tail fits the limits.
		discardedOutput bool
		// TextDecoder, like upstream's: a leading BOM is dropped.
		decoder = utf8stream.Decoder{StripBOM: true}
		// Output after the command ended (from processes it left running) is not part of its result.
		acceptingOutput atomic.Bool
	)
	acceptingOutput.Store(true)
	ensureTempFile := func() {
		if tempFile != nil {
			return
		}
		tempFile = outputfiles.OpenStream("pi-bash", ".log")
		for _, chunk := range outputChunks {
			tempFile.WriteString(chunk)
		}
	}
	closeTempFile := func() outputfiles.StreamResult {
		if tempFile == nil {
			return outputfiles.StreamResult{}
		}
		return tempFile.Close()
	}
	// Unfinished escape sequence at the end of the previous chunk, completed by the next chunk (bash-executor.ts pendingAnsi).
	pendingAnsi := ""
	appendText := func(rawText string) {
		chunk := strings.ReplaceAll(text.SanitizeBinaryOutput(text.StripANSIString(rawText)), "\r", "")
		if chunk == "" {
			return
		}
		if totalBytes > DefaultMaxBytesUpstream {
			ensureTempFile()
		}
		if tempFile != nil {
			tempFile.WriteString(chunk)
		}
		outputChunks = append(outputChunks, chunk)
		outputBytes += jsLength(chunk)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			outputBytes -= jsLength(outputChunks[0])
			outputChunks = outputChunks[1:]
			discardedOutput = true
		}
		if opts.OnChunk != nil {
			opts.OnChunk(chunk)
		}
	}
	onData := func(data []byte) {
		if !acceptingOutput.Load() {
			return
		}
		totalBytes += len(data)
		decoded := decoder.Decode(data, true)
		if pendingAnsi != "" {
			decoded = pendingAnsi + decoded
		}
		var complete string
		complete, pendingAnsi = SplitIncompleteAnsiSuffix(decoded)
		appendText(complete)
	}
	flushOutput := func() {
		rest := pendingAnsi + decoder.Decode(nil, false)
		pendingAnsi = ""
		appendText(rest)
	}
	finish := func(exitCode *int, cancelled bool) BashResult {
		fullOutput := strings.Join(outputChunks, "")
		tr := TruncateTail(fullOutput, TruncationOptions{})
		if tr.Truncated {
			ensureTempFile()
		}
		// A failed file is dropped: the result keeps the in-memory tail without a path.
		saved := closeTempFile()
		truncated := discardedOutput || tr.Truncated
		output := fullOutput
		if tr.Truncated {
			// The kept tail is a substring of the joined window; copy it so the result does not hold the whole window.
			output = strings.Clone(tr.Content)
		}
		var fullOutputError string
		// The file opens on raw bytes, before ANSI codes and carriage returns are stripped, so it can fail
		// for output that turns out not to be truncated; nothing is missing then.
		if truncated && saved.Failed() {
			fullOutputError = saved.Error
		}
		return BashResult{
			Output:          output,
			ExitCode:        exitCode,
			Cancelled:       cancelled,
			Truncated:       truncated,
			FullOutputPath:  saved.Path,
			FullOutputError: fullOutputError,
		}
	}

	result, err := operations.Exec(ctx, command, cwd, BashOperationsExecOptions{OnData: onData})
	// The result is decided when the command ends: closing the file below takes time, and an abort or
	// output during it must not change the result.
	acceptingOutput.Store(false)
	cancelled := ctx.Err() != nil
	if err != nil && !cancelled {
		closeTempFile()
		return BashResult{}, err
	}
	flushOutput()
	exitCode := result.ExitCode
	if cancelled {
		exitCode = nil
	}
	return finish(exitCode, cancelled), nil
}

// jsLength is JavaScript String.length: UTF-16 code units.
func jsLength(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
