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
	"os"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/outputfiles"
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
func ExecuteBashWithOperations(ctx context.Context, command, cwd string, operations BashOperations, opts BashExecOptions) (BashResult, error) {
	const maxOutputBytes = DefaultMaxBytesUpstream * 2
	var (
		outputChunks   []string
		outputBytes    int
		totalBytes     int
		tempFilePath   string
		tempFile       *os.File
		tempFileOpened bool
		// TextDecoder, like upstream's: a leading BOM is dropped.
		decoder = utf8StreamDecoder{stripBOM: true}
	)
	ensureTempFile := func() {
		if tempFileOpened {
			return
		}
		tempFileOpened = true
		path, f, err := outputfiles.CreateStream("pi-bash", ".log")
		tempFilePath = path
		if err == nil {
			tempFile = f
			for _, chunk := range outputChunks {
				_, _ = f.WriteString(chunk)
			}
		}
	}
	// Unfinished escape sequence at the end of the previous chunk, completed by the next chunk (bash-executor.ts pendingAnsi).
	pendingAnsi := ""
	appendText := func(rawText string) {
		text := strings.ReplaceAll(SanitizeBinaryOutput(string(StripANSI([]byte(rawText)))), "\r", "")
		if text == "" {
			return
		}
		if totalBytes > DefaultMaxBytesUpstream {
			ensureTempFile()
		}
		if tempFile != nil {
			_, _ = tempFile.WriteString(text)
		}
		outputChunks = append(outputChunks, text)
		outputBytes += jsLength(text)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			outputBytes -= jsLength(outputChunks[0])
			outputChunks = outputChunks[1:]
		}
		if opts.OnChunk != nil {
			opts.OnChunk(text)
		}
	}
	onData := func(data []byte) {
		totalBytes += len(data)
		var complete string
		complete, pendingAnsi = SplitIncompleteAnsiSuffix(pendingAnsi + decoder.decode(data, true))
		appendText(complete)
	}
	flushOutput := func() {
		rest := pendingAnsi + decoder.decode(nil, false)
		pendingAnsi = ""
		appendText(rest)
	}
	finish := func(exitCode *int, cancelled bool) BashResult {
		fullOutput := strings.Join(outputChunks, "")
		tr := TruncateTail(fullOutput, TruncationOptions{})
		if tr.Truncated {
			ensureTempFile()
		}
		if tempFile != nil {
			_ = tempFile.Close()
		}
		output := fullOutput
		if tr.Truncated {
			output = tr.Content
		}
		return BashResult{Output: output, ExitCode: exitCode, Cancelled: cancelled, Truncated: tr.Truncated, FullOutputPath: tempFilePath}
	}

	result, err := operations.Exec(ctx, command, cwd, BashOperationsExecOptions{OnData: onData})
	cancelled := ctx.Err() != nil
	if err != nil && !cancelled {
		if tempFile != nil {
			_ = tempFile.Close()
		}
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
