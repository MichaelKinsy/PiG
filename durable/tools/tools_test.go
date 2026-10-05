package tools

// Ported from packages/durable/test/tools.test.ts at v1.0.0 (itself ported from
// packages/agent/test/harness/tools.test.ts and adapted to ToolRegistration:
// tools take the environment from api.Env(), stream through api.Output(), and
// report notices as diagnostics instead of content text). Each test name is the
// upstream case title.

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var background = context.Background()

func must[T any](value T, err error) T {
	if err != nil {
		panic("unexpected error: " + err.Error())
	}
	return value
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createEnv(t *testing.T) *envnode.NodeExecutionEnv {
	t.Helper()
	return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()})
}

// fakeAPI is a minimal execution API: the environment, collected output and
// diagnostics, and nothing durable. Calling any other operation panics on the
// embedded nil interface.
type fakeAPI struct {
	durable.ToolExecutionApi
	env         env.ExecutionEnv
	mu          sync.Mutex
	output      []string
	skipped     []env.ShellOutputSkip
	window      *env.ShellOutputWindow
	diagnostics []durable.ToolDiagnostic
}

// OutputWindow is the window the test offers the tool; nil as for a tool that keeps the head of its output.
func (api *fakeAPI) OutputWindow() *env.ShellOutputWindow { return api.window }

func (api *fakeAPI) OutputSkipping(chunk any, skipped env.ShellOutputSkip) {
	api.Output(chunk)
	api.mu.Lock()
	defer api.mu.Unlock()
	api.skipped = append(api.skipped, skipped)
}

func (api *fakeAPI) Env() env.ExecutionEnv { return api.env }

func (api *fakeAPI) Output(chunk any) {
	api.mu.Lock()
	defer api.mu.Unlock()
	switch typed := chunk.(type) {
	case string:
		api.output = append(api.output, typed)
	case []byte:
		api.output = append(api.output, string(typed))
	}
}

func (api *fakeAPI) Diagnostic(diagnostic durable.ToolDiagnostic) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.diagnostics = append(api.diagnostics, diagnostic)
}

type runResult struct {
	durable.ToolExecutionResult
	output   []string
	reported []durable.ToolDiagnostic
}

func run(tool *durable.ToolRegistration, args any, executionEnv env.ExecutionEnv, ctx context.Context) (runResult, error) {
	api := &fakeAPI{env: executionEnv}
	result, err := tool.Execute(ctx, args, api)
	return runResult{result, api.output, api.diagnostics}, err
}

func mustRun(t *testing.T, tool *durable.ToolRegistration, args any, executionEnv env.ExecutionEnv) runResult {
	t.Helper()
	result, err := run(tool, args, executionEnv, background)
	mustDo(t, err)
	return result
}

// runFailing runs a tool expected to fail; it returns the error with what the
// tool streamed and reported first.
func runFailing(t *testing.T, tool *durable.ToolRegistration, args any, executionEnv env.ExecutionEnv) (runResult, error) {
	t.Helper()
	result, err := run(tool, args, executionEnv, background)
	if err == nil {
		t.Fatal("expected the tool to fail")
	}
	return result, err
}

func expectFailure(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error = %v, want one matching %q", err, pattern)
	}
}

func textOutput(result durable.ToolExecutionResult) string {
	var texts []string
	for _, part := range result.Content {
		if text, ok := part.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func diagnosticText(result durable.ToolExecutionResult) string {
	var messages []string
	for _, diagnostic := range result.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	return strings.Join(messages, "\n")
}

func readTextFile(t *testing.T, executionEnv env.ExecutionEnv, path string) string {
	t.Helper()
	return must(executionEnv.ReadTextFile(background, path))
}

func writeText(t *testing.T, executionEnv env.ExecutionEnv, path string, content any) {
	t.Helper()
	mustDo(t, executionEnv.WriteFile(background, path, content))
}

func lines(count int, format func(int) string, separator string) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = format(i + 1)
	}
	return strings.Join(parts, separator)
}

// subclasses of NodeExecutionEnv. Each sets Self so the environment's own calls
// reach its overrides, as they do in a TypeScript subclass.

type slowReadEnv struct{ *envnode.NodeExecutionEnv }

func newSlowReadEnv(cwd string) *slowReadEnv {
	slow := &slowReadEnv{envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd})}
	slow.Self = slow
	return slow
}

func (slow *slowReadEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	time.Sleep(20 * time.Millisecond)
	return slow.NodeExecutionEnv.ReadTextFile(ctx, path)
}

type blockingWriteEnv struct {
	*envnode.NodeExecutionEnv
	firstWriteStarted  chan struct{}
	finishFirstWrite   chan struct{}
	secondWriteStarted chan struct{}
	startedOnce        sync.Once
	secondOnce         sync.Once
	id                 string
}

func newBlockingWriteEnv(cwd string) *blockingWriteEnv {
	blocking := &blockingWriteEnv{
		NodeExecutionEnv:   envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}),
		firstWriteStarted:  make(chan struct{}),
		finishFirstWrite:   make(chan struct{}),
		secondWriteStarted: make(chan struct{}),
	}
	blocking.Self = blocking
	return blocking
}

func (blocking *blockingWriteEnv) Id() string {
	if blocking.id != "" {
		return blocking.id
	}
	return blocking.NodeExecutionEnv.Id()
}

func (blocking *blockingWriteEnv) WriteFile(ctx context.Context, path string, content any) error {
	switch content {
	case "first\n":
		blocking.startedOnce.Do(func() { close(blocking.firstWriteStarted) })
		<-blocking.finishFirstWrite
	case "second\n":
		blocking.secondOnce.Do(func() { close(blocking.secondWriteStarted) })
	}
	return blocking.NodeExecutionEnv.WriteFile(ctx, path, content)
}

func (blocking *blockingWriteEnv) secondWriteHasStarted() bool {
	select {
	case <-blocking.secondWriteStarted:
		return true
	default:
		return false
	}
}

type blockingEditEnv struct {
	*envnode.NodeExecutionEnv
	firstEditWriteStarted  chan struct{}
	finishFirstEditWrite   chan struct{}
	firstEditWriteSettled  bool
	secondEditWriteStarted bool
	mu                     sync.Mutex
}

func newBlockingEditEnv(cwd string) *blockingEditEnv {
	blocking := &blockingEditEnv{
		NodeExecutionEnv:      envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}),
		firstEditWriteStarted: make(chan struct{}),
		finishFirstEditWrite:  make(chan struct{}),
	}
	blocking.Self = blocking
	return blocking
}

func (blocking *blockingEditEnv) WriteFile(ctx context.Context, path string, content any) error {
	switch content {
	case "ALPHA\nbeta\n":
		close(blocking.firstEditWriteStarted)
		<-blocking.finishFirstEditWrite
		err := blocking.NodeExecutionEnv.WriteFile(background, path, content)
		blocking.mu.Lock()
		blocking.firstEditWriteSettled = true
		blocking.mu.Unlock()
		return err
	case "ALPHA\nBETA\n", "alpha\nBETA\n":
		blocking.mu.Lock()
		blocking.secondEditWriteStarted = true
		blocking.mu.Unlock()
	}
	return blocking.NodeExecutionEnv.WriteFile(ctx, path, content)
}

const truncatedOutputLines = durable.DEFAULT_MAX_LINES + 1

type timeoutOutputEnv struct{ *envnode.NodeExecutionEnv }

func (timeout *timeoutOutputEnv) Exec(ctx context.Context, _ any, options *env.ShellExecOptions) (env.ShellExecResult, error) {
	output := lines(truncatedOutputLines, func(i int) string { return "line-" + itoa(i) }, "\n") + "\n"
	spillPath := must(timeout.CreateTempFile(ctx, &env.CreateTempFileOptions{Prefix: "timeout-", Suffix: ".log"}))
	if err := timeout.WriteFile(ctx, spillPath, output); err != nil {
		return env.ShellExecResult{}, err
	}
	if options != nil && options.OnOutput != nil {
		options.OnOutput(ctx, output, env.ShellOutputInfo{Stream: env.ShellStdout})
	}
	timeoutText := "timeout:undefined"
	if options != nil && options.Timeout != nil {
		timeoutText = "timeout:" + ftoa(*options.Timeout)
	}
	failure := env.NewExecutionError(env.ExecutionErrorTimeout, timeoutText, nil)
	failure.SpillPath = spillPath
	return env.ShellExecResult{}, failure
}

func TestDurableToolsFailWithAnOrdinaryErrorWhenNoEnvironmentIsConfigured(t *testing.T) {
	_, err := run(CreateReadTool(), map[string]any{"path": "x"}, nil, background)
	expectFailure(t, err, "No execution environment")
}

func TestReadDetectsTheCompleteGIFSignature(t *testing.T) {
	for _, signature := range []string{"GIF87a", "GIF89a"} {
		if got := detectSupportedImageMimeType([]byte(signature)); got != "image/gif" {
			t.Errorf("%s: mime = %q, want image/gif", signature, got)
		}
	}
}

// Not an upstream case: pins the read.ts userLimitedLines boundary. A limit
// that ends exactly at the last line reports no continuation; one line short
// reports a single remaining line.
func TestReadLimitEndingAtTheLastLineReportsNoContinuation(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "ten.txt", lines(10, func(i int) string { return "Line " + itoa(i) }, "\n"))
	for _, tc := range []struct {
		limit float64
		want  string
	}{
		{10, ""},
		{9, "1 more lines in file. Use offset=10 to continue."},
	} {
		result := mustRun(t, CreateReadTool(), map[string]any{"path": "ten.txt", "limit": tc.limit}, executionEnv)
		if got := diagnosticText(result.ToolExecutionResult); got != tc.want {
			t.Errorf("limit %v: diagnostics = %q, want %q", tc.limit, got, tc.want)
		}
	}
}

func TestReadReadsTextWithOffsetsAndLimitsAndReportsContinuationAsADiagnostic(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "test.txt", lines(100, func(i int) string { return "Line " + itoa(i) }, "\n"))
	result := mustRun(t, CreateReadTool(), map[string]any{"path": "test.txt", "offset": 41.0, "limit": 20.0}, executionEnv)
	output := textOutput(result.ToolExecutionResult)
	for _, want := range []string{"Line 41", "Line 60"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Line 40", "Line 61", "more lines"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("output contains %q", unwanted)
		}
	}
	if got := diagnosticText(result.ToolExecutionResult); got != "40 more lines in file. Use offset=61 to continue." {
		t.Fatalf("diagnostics = %q", got)
	}
}

func TestReadTruncatesLargeTextByLineCount(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "large.txt", lines(2500, func(i int) string { return "Line " + itoa(i) }, "\n"))
	result := mustRun(t, CreateReadTool(), map[string]any{"path": "large.txt"}, executionEnv)
	if got := diagnosticText(result.ToolExecutionResult); got != "Showing lines 1-2000 of 2500. Use offset=2001 to continue." {
		t.Fatalf("diagnostics = %q", got)
	}
	if result.Diagnostics[0].Code != "truncated" {
		t.Fatalf("code = %q", result.Diagnostics[0].Code)
	}
	truncation := result.Details.(map[string]any)["truncation"].(map[string]any)
	want := map[string]any{"truncated": true, "truncatedBy": "lines", "totalLines": 2500.0, "outputLines": 2000.0}
	for key, value := range want {
		if truncation[key] != value {
			t.Errorf("truncation.%s = %v, want %v", key, truncation[key], value)
		}
	}
}

func TestReadDoesNotCountATrailingNewlineAsAnExtraLineAtTheTruncationLimit(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "exact.txt", lines(2000, func(int) string { return "x" }, "\n")+"\n")
	result := mustRun(t, CreateReadTool(), map[string]any{"path": "exact.txt"}, executionEnv)
	if result.HasDetails || result.Details != nil {
		t.Fatalf("details = %v", result.Details)
	}
	if result.Diagnostics == nil || len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want empty", result.Diagnostics)
	}
}

func TestReadShowsTheStartOfALineLongerThanTheByteLimit(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "long.txt", strings.Repeat("é", 40_000)+"\nnext\n")
	result := mustRun(t, CreateReadTool(), map[string]any{"path": "long.txt"}, executionEnv)
	// Two-byte characters: the cut lands on a character boundary at or below the limit.
	if got := textOutput(result.ToolExecutionResult); got != strings.Repeat("é", 25_600) {
		t.Fatalf("text has %d characters", len([]rune(got)))
	}
	if got, want := diagnosticText(result.ToolExecutionResult), "Line 1 is 78.1KB, exceeds the 50.0KB limit; showing its first 50.0KB. Use bash: sed -n '1p' long.txt | tail -c +51201"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
	truncation := result.Details.(map[string]any)["truncation"].(map[string]any)
	want := map[string]any{"truncated": true, "firstLineExceedsLimit": true, "outputBytes": 51_200.0, "outputLines": 1.0}
	for key, value := range want {
		if truncation[key] != value {
			t.Errorf("truncation.%s = %v, want %v", key, truncation[key], value)
		}
	}
	if _, hasContent := truncation["content"]; hasContent {
		t.Error("truncation carries content")
	}
}

func TestReadRejectsOffsetsBeyondTheFile(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "short.txt", "one\ntwo\nthree")
	_, err := run(CreateReadTool(), map[string]any{"path": "short.txt", "offset": 100.0}, executionEnv, background)
	expectFailure(t, err, regexp.QuoteMeta("Offset 100 is beyond end of file (3 lines total)"))
}

func TestReadReportsImagesByContentAsUnsupported(t *testing.T) {
	executionEnv := createEnv(t)
	png := must(base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg=="))
	writeText(t, executionEnv, "image.txt", png)
	result := mustRun(t, CreateReadTool(), map[string]any{"path": "image.txt"}, executionEnv)
	if result.Content == nil || len(result.Content) != 0 || result.IsError == nil || !*result.IsError {
		t.Fatalf("result = %+v", result.ToolExecutionResult)
	}
	if got := diagnosticText(result.ToolExecutionResult); got != "image.txt is an image (image/png); reading images is not supported" {
		t.Fatalf("diagnostics = %q", got)
	}
}

func TestWriteWritesFilesAndCreatesParentDirectories(t *testing.T) {
	executionEnv := createEnv(t)
	result := mustRun(t, CreateWriteTool(), map[string]any{"path": "nested/dir/file.txt", "content": "hello"}, executionEnv)
	if got := textOutput(result.ToolExecutionResult); got != "Successfully wrote to nested/dir/file.txt" {
		t.Fatalf("output = %q", got)
	}
	if got := readTextFile(t, executionEnv, "nested/dir/file.txt"); got != "hello" {
		t.Fatalf("file = %q", got)
	}
}

func TestWriteKeepsTheMutationQueueLockedUntilAnAbortedWriteSettles(t *testing.T) {
	executionEnv := newBlockingWriteEnv(t.TempDir())
	tool := CreateWriteTool()
	ctx, cancel := context.WithCancel(background)
	firstDone := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "file.txt", "content": "first\n"}, executionEnv, ctx)
		firstDone <- err
	}()
	<-executionEnv.firstWriteStarted
	cancel()
	secondDone := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "file.txt", "content": "second\n"}, executionEnv, background)
		secondDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if executionEnv.secondWriteHasStarted() {
		t.Fatal("the second write started while the first was pending")
	}
	close(executionEnv.finishFirstWrite)
	if err := <-firstDone; err == nil {
		t.Fatal("the aborted write succeeded")
	}
	mustDo(t, <-secondDone)
	if got := readTextFile(t, executionEnv, "file.txt"); got != "second\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditAppliesDisjointEditsAndReturnsBothDiffFormats(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "edit.txt", "alpha\nbeta\ngamma\ndelta\n")
	result := mustRun(t, CreateEditTool(), map[string]any{"path": "edit.txt", "edits": []any{
		map[string]any{"oldText": "alpha\n", "newText": "ALPHA\n"},
		map[string]any{"oldText": "gamma\n", "newText": "GAMMA\n"},
	}}, executionEnv)
	details := result.Details.(map[string]any)
	if got := textOutput(result.ToolExecutionResult); got != "Successfully replaced 2 block(s) in edit.txt." {
		t.Fatalf("output = %q", got)
	}
	diff := details["diff"].(string)
	if !strings.Contains(diff, "ALPHA") || !strings.Contains(diff, "GAMMA") {
		t.Fatalf("diff = %q", diff)
	}
	if got := applyPatch(t, "alpha\nbeta\ngamma\ndelta\n", details["patch"].(string)); got != "ALPHA\nbeta\nGAMMA\ndelta\n" {
		t.Fatalf("applying the patch gives %q", got)
	}
	if got := readTextFile(t, executionEnv, "edit.txt"); got != "ALPHA\nbeta\nGAMMA\ndelta\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditRepairsEditsSentAsAJSONStringASingleObjectOrTopLevelOldTextNewTextWithoutMutatingThem(t *testing.T) {
	prepare := CreateEditTool().PrepareArguments
	edit := map[string]any{"oldText": "a", "newText": "b"}
	editJSON := `{"oldText":"a","newText":"b"}`
	check := func(input, want any) {
		t.Helper()
		got, err := prepare(input)
		mustDo(t, err)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prepare(%v) = %v, want %v", input, got, want)
		}
	}
	asString := map[string]any{"path": "f", "edits": "[" + editJSON + "]"}
	check(asString, map[string]any{"path": "f", "edits": []any{edit}})
	if asString["edits"] != "["+editJSON+"]" {
		t.Fatalf("prepareArguments mutated its input: %v", asString)
	}
	check(map[string]any{"path": "f", "edits": editJSON}, map[string]any{"path": "f", "edits": []any{edit}})
	check(map[string]any{"path": "f", "edits": edit}, map[string]any{"path": "f", "edits": []any{edit}})
	check(map[string]any{"path": "f", "edits": []any{edit}, "oldText": "c", "newText": "d"},
		map[string]any{"path": "f", "edits": []any{edit, map[string]any{"oldText": "c", "newText": "d"}}})
	check(map[string]any{"path": "f", "edits": "not json"}, map[string]any{"path": "f", "edits": "not json"})
}

func TestEditMatchesAllEditsAgainstTheOriginalAndRejectsOverlaps(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "edit.txt", "one\ntwo\nthree\n")
	_, err := run(CreateEditTool(), map[string]any{"path": "edit.txt", "edits": []any{
		map[string]any{"oldText": "one\ntwo\n", "newText": "ONE\nTWO\n"},
		map[string]any{"oldText": "two\nthree\n", "newText": "TWO\nTHREE\n"},
	}}, executionEnv, background)
	expectFailure(t, err, "overlap")
	if got := readTextFile(t, executionEnv, "edit.txt"); got != "one\ntwo\nthree\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditRejectsMissingAndDuplicateTargetText(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "edit.txt", "foo foo foo")
	tool := CreateEditTool()
	_, err := run(tool, map[string]any{"path": "edit.txt", "edits": []any{map[string]any{"oldText": "bar", "newText": "baz"}}}, executionEnv, background)
	expectFailure(t, err, "Could not find the exact text")
	_, err = run(tool, map[string]any{"path": "edit.txt", "edits": []any{map[string]any{"oldText": "foo", "newText": "bar"}}}, executionEnv, background)
	expectFailure(t, err, "Found 3 occurrences")
}

func TestEditKeepsTheMutationQueueLockedUntilAnAbortedEditWriteSettles(t *testing.T) {
	executionEnv := newBlockingEditEnv(t.TempDir())
	writeText(t, executionEnv, "file.txt", "alpha\nbeta\n")
	tool := CreateEditTool()
	ctx, cancel := context.WithCancel(background)
	firstDone := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "file.txt", "edits": []any{map[string]any{"oldText": "alpha", "newText": "ALPHA"}}}, executionEnv, ctx)
		firstDone <- err
	}()
	<-executionEnv.firstEditWriteStarted
	cancel()
	secondDone := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "file.txt", "edits": []any{map[string]any{"oldText": "beta", "newText": "BETA"}}}, executionEnv, background)
		secondDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	executionEnv.mu.Lock()
	started := executionEnv.secondEditWriteStarted
	executionEnv.mu.Unlock()
	if started {
		t.Fatal("the second edit wrote while the first was pending")
	}
	close(executionEnv.finishFirstEditWrite)
	expectFailure(t, <-firstDone, "^Operation aborted$")
	mustDo(t, <-secondDone)
	executionEnv.mu.Lock()
	settled := executionEnv.firstEditWriteSettled
	executionEnv.mu.Unlock()
	if !settled {
		t.Fatal("the first edit write never settled")
	}
	if got := readTextFile(t, executionEnv, "file.txt"); got != "ALPHA\nBETA\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditSerializesConcurrentEditsThroughCanonicalAndSymlinkPaths(t *testing.T) {
	executionEnv := newSlowReadEnv(t.TempDir())
	writeText(t, executionEnv, "target.txt", "alpha\nbeta\ngamma\n")
	testenv.Symlink(t, "target.txt", filepath.Join(executionEnv.Cwd(), "link.txt"))
	tool := CreateEditTool()
	var wg sync.WaitGroup
	for _, call := range []struct{ path, old, new string }{{"target.txt", "alpha", "ALPHA"}, {"link.txt", "beta", "BETA"}} {
		wg.Go(func() {
			if _, err := run(tool, map[string]any{"path": call.path, "edits": []any{map[string]any{"oldText": call.old, "newText": call.new}}}, executionEnv, background); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := readTextFile(t, executionEnv, "target.txt"); got != "ALPHA\nBETA\ngamma\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditSerializesEditsOfOneFileAcrossEnvironmentObjectsOfOneFileSystem(t *testing.T) {
	dir := t.TempDir()
	first, second := newSlowReadEnv(dir), newSlowReadEnv(dir)
	writeText(t, first, "file.txt", "alpha\nbeta\n")
	tool := CreateEditTool()
	var wg sync.WaitGroup
	for _, call := range []struct {
		executionEnv *slowReadEnv
		old, new     string
	}{{first, "alpha", "ALPHA"}, {second, "beta", "BETA"}} {
		wg.Go(func() {
			if _, err := run(tool, map[string]any{"path": "file.txt", "edits": []any{map[string]any{"oldText": call.old, "newText": call.new}}}, call.executionEnv, background); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := readTextFile(t, first, "file.txt"); got != "ALPHA\nBETA\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditSerializesANewFileCreatedThroughASymlinkedDirectoryWithItsCanonicalPath(t *testing.T) {
	executionEnv := newBlockingWriteEnv(t.TempDir())
	mustDo(t, os.Mkdir(filepath.Join(executionEnv.Cwd(), "real"), 0o755))
	testenv.RequireDirectoryLink(t, filepath.Join(executionEnv.Cwd(), "real"), filepath.Join(executionEnv.Cwd(), "link"))
	tool := CreateWriteTool()
	firstDone := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "link/new.txt", "content": "first\n"}, executionEnv, background)
		firstDone <- err
	}()
	<-executionEnv.firstWriteStarted
	secondDone := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "real/new.txt", "content": "second\n"}, executionEnv, background)
		secondDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if executionEnv.secondWriteHasStarted() {
		t.Fatal("the second write started while the first was pending")
	}
	close(executionEnv.finishFirstWrite)
	mustDo(t, <-firstDone)
	mustDo(t, <-secondDone)
	if got := readTextFile(t, executionEnv, "real/new.txt"); got != "second\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditKeysAMissingFileWhoseNameContainsABackslashLikeTheCreatedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash separates path segments on Windows")
	}
	executionEnv := createEnv(t)
	created, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := withFileMutationQueue(background, executionEnv, `a\b.txt`, func() (struct{}, error) {
			if err := executionEnv.WriteFile(background, `a\b.txt`, "first\n"); err != nil {
				return struct{}{}, err
			}
			close(created)
			<-release
			return struct{}{}, nil
		})
		firstDone <- err
	}()
	<-created
	enteredFlag := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		_, err := withFileMutationQueue(background, executionEnv, `a\b.txt`, func() (struct{}, error) {
			close(enteredFlag)
			return struct{}{}, nil
		})
		secondDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-enteredFlag:
		t.Fatal("the second mutation entered while the first held the file")
	default:
	}
	close(release)
	mustDo(t, <-firstDone)
	mustDo(t, <-secondDone)
	select {
	case <-enteredFlag:
	default:
		t.Fatal("the second mutation never entered")
	}
}

func TestEditDoesNotSerializeTheSamePathOnDifferentFileSystems(t *testing.T) {
	dir := t.TempDir()
	local := newBlockingWriteEnv(dir)
	other := newBlockingWriteEnv(dir)
	other.id = "other"
	tool := CreateWriteTool()
	blocked := make(chan error, 1)
	go func() {
		_, err := run(tool, map[string]any{"path": "file.txt", "content": "first\n"}, local, background)
		blocked <- err
	}()
	<-local.firstWriteStarted
	mustDo(t, func() error {
		_, err := run(tool, map[string]any{"path": "file.txt", "content": "second\n"}, other, background)
		return err
	}())
	if !other.secondWriteHasStarted() {
		t.Fatal("the other file system's write did not run")
	}
	close(local.finishFirstWrite)
	mustDo(t, <-blocked)
}

func TestEditEditsRegularFilesThroughSymlinks(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "target.txt", "before\n")
	testenv.Symlink(t, "target.txt", filepath.Join(executionEnv.Cwd(), "link.txt"))
	mustRun(t, CreateEditTool(), map[string]any{"path": "link.txt", "edits": []any{map[string]any{"oldText": "before", "newText": "after"}}}, executionEnv)
	if got := readTextFile(t, executionEnv, "target.txt"); got != "after\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditPreservesBOMAndCRLFLineEndings(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "edit.txt", "\uFEFFone\r\ntwo\r\n")
	mustRun(t, CreateEditTool(), map[string]any{"path": "edit.txt", "edits": []any{map[string]any{"oldText": "two", "newText": "TWO"}}}, executionEnv)
	if got := readTextFile(t, executionEnv, "edit.txt"); got != "\uFEFFone\r\nTWO\r\n" {
		t.Fatalf("file = %q", got)
	}
}

// skippingEnv reports the window it was given and delivers one chunk that follows omitted output.
type skippingEnv struct {
	*envnode.NodeExecutionEnv
	skipped  env.ShellOutputSkip
	received *env.ShellOutputWindow
}

func (skipping *skippingEnv) Exec(ctx context.Context, _ any, options *env.ShellExecOptions) (env.ShellExecResult, error) {
	skipping.received = options.Window
	options.OnOutput(ctx, "tail\n", env.ShellOutputInfo{Stream: env.ShellStdout, Skipped: &skipping.skipped})
	return env.ShellExecResult{}, nil
}

func TestBashPassesTheRetainedWindowToTheEnvironmentAndForwardsWhatItSkipped(t *testing.T) {
	// upstream: packages/durable/test/tools.test.ts:523
	window := env.ShellOutputWindow{MaxBytes: 4, MaxLines: 1, MinIntervalMs: 100, BytesPerSecond: 1024}
	executionEnv := &skippingEnv{
		NodeExecutionEnv: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()}),
		skipped:          env.ShellOutputSkip{Bytes: 6, Newlines: 2, EndsWithNewline: true},
	}
	api := &fakeAPI{env: executionEnv, window: &window}
	if _, err := CreateBashTool(nil).Execute(background, map[string]any{"command": "anything"}, api); err != nil {
		t.Fatal(err)
	}
	if executionEnv.received == nil || *executionEnv.received != window {
		t.Fatalf("received window %v, want %+v", executionEnv.received, window)
	}
	if !slices.Equal(api.output, []string{"tail\n"}) || !slices.Equal(api.skipped, []env.ShellOutputSkip{executionEnv.skipped}) {
		t.Fatalf("output %q skipped %v, want one chunk with the skip", api.output, api.skipped)
	}
}

func TestBashStreamsCombinedStdoutAndStderrAndReturnsNoContentOfItsOwn(t *testing.T) {
	result := mustRun(t, CreateBashTool(nil), map[string]any{"command": "printf out; printf err >&2"}, createEnv(t))
	output := strings.Join(result.output, "")
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Fatalf("output = %q", output)
	}
	if result.Content != nil {
		t.Fatalf("content = %v", result.Content)
	}
}

func TestBashThrowsOnNonzeroExitsAndTimeoutsAfterStreamingTheOutput(t *testing.T) {
	executionEnv := createEnv(t)
	tool := CreateBashTool(nil)
	failed, err := runFailing(t, tool, map[string]any{"command": "printf failed; exit 7"}, executionEnv)
	if err.Error() != "Command exited with code 7" {
		t.Fatalf("error = %v", err)
	}
	if got := strings.Join(failed.output, ""); got != "failed" {
		t.Fatalf("output = %q", got)
	}
	_, err = runFailing(t, tool, map[string]any{"command": "sleep 2", "timeout": 0.01}, executionEnv)
	if err.Error() != "Command timed out after 0.01 seconds" {
		t.Fatalf("error = %v", err)
	}
}

func TestBashReportsTheSpillOfACommandThatTimesOut(t *testing.T) {
	executionEnv := &timeoutOutputEnv{envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()})}
	failed, err := runFailing(t, CreateBashTool(nil), map[string]any{"command": "emit-output-then-time-out", "timeout": 0.05}, executionEnv)
	if err.Error() != "Command timed out after 0.05 seconds" {
		t.Fatalf("error = %v", err)
	}
	match := regexp.MustCompile(`^Full output: (.+)$`).FindStringSubmatch(failed.reported[0].Message)
	if match == nil {
		t.Fatalf("reported = %+v", failed.reported)
	}
	fullOutput := readTextFile(t, executionEnv, match[1])
	if !strings.Contains(fullOutput, "line-1\nline-2") || !strings.Contains(fullOutput, "line-2000\nline-"+itoa(truncatedOutputLines)) {
		t.Fatal("the spill does not hold the complete output")
	}
}

func TestBashPreparesCommandCwdAndAnExplicitEnvironmentWithTheCallsApi(t *testing.T) {
	executionEnv := envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir(), ShellEnv: map[string]string{"PI_BASH_PREPARE_INHERITED": "inherited"}})
	mustDo(t, executionEnv.CreateDir(background, "workspace", nil))
	workspace := executionEnv.Cwd() + "/workspace"
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	var receivedEnv env.ExecutionEnv
	var receivedCtx context.Context
	tool := CreateBashTool(&BashToolOptions{
		CommandPrefix: "prefix=ready",
		Prepare: func(callContext context.Context, execution *BashExecution, api durable.ToolExecutionApi) error {
			receivedEnv = api.Env()
			receivedCtx = callContext
			execution.Cwd = workspace
			execution.Env = map[string]string{"PI_BASH_PREPARE_EXPLICIT": "explicit"}
			execution.InheritEnv = false
			execution.Command += "\n: > prepared-cwd\nprintf '%s:%s:%s' \"$prefix\" \"${PI_BASH_PREPARE_INHERITED-}\" \"$PI_BASH_PREPARE_EXPLICIT\""
			// Git Bash on Windows reports $PWD as an MSYS path, so only POSIX compares it.
			if runtime.GOOS != "windows" {
				execution.Command += "\nprintf ':%s' \"$PWD\""
			}
			return nil
		},
	})
	result, err := run(tool, map[string]any{"command": ":"}, executionEnv, ctx)
	mustDo(t, err)
	if receivedEnv != env.ExecutionEnv(executionEnv) {
		t.Fatal("prepare did not receive the call's environment")
	}
	if receivedCtx != ctx {
		t.Fatal("prepare did not receive the call's context")
	}
	want := "ready::explicit"
	if runtime.GOOS != "windows" {
		want += ":" + must(executionEnv.CanonicalPath(background, workspace))
	}
	if got := strings.Join(result.output, ""); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if !must(executionEnv.Exists(background, workspace+"/prepared-cwd")) {
		t.Fatal("the command did not run in the prepared cwd")
	}
}

func TestBashSupportsCommandPrefixes(t *testing.T) {
	result := mustRun(t, CreateBashTool(&BashToolOptions{CommandPrefix: "value=hello"}), map[string]any{"command": "printf $value"}, createEnv(t))
	if got := strings.Join(result.output, ""); got != "hello" {
		t.Fatalf("output = %q", got)
	}
}

func TestBashStreamsEveryByteAndSpillsCompleteOutputBeyondTheDefaultLimits(t *testing.T) {
	executionEnv := createEnv(t)
	result := mustRun(t, CreateBashTool(nil), map[string]any{"command": "i=1; while [ $i -le 3000 ]; do echo line-$i; i=$((i + 1)); done"}, executionEnv)
	expected := lines(3000, func(i int) string { return "line-" + itoa(i) + "\n" }, "")
	if got := strings.Join(result.output, ""); got != expected {
		t.Fatalf("streamed %d bytes, want %d", len(got), len(expected))
	}
	match := regexp.MustCompile(`^Full output: (.+)$`).FindStringSubmatch(result.reported[0].Message)
	if match == nil {
		t.Fatalf("reported = %+v", result.reported)
	}
	if got := readTextFile(t, executionEnv, match[1]); got != expected {
		t.Fatalf("spill has %d bytes, want %d", len(got), len(expected))
	}
}

func TestBashDoesNotSpillOutputWithinTheLimits(t *testing.T) {
	result := mustRun(t, CreateBashTool(nil), map[string]any{"command": "printf small"}, createEnv(t))
	if len(result.reported) != 0 {
		t.Fatalf("reported = %+v", result.reported)
	}
}

// applyPatch applies a single-file unified patch, as jsdiff's applyPatch does
// for the patches the edit tool makes: every hunk's context and removed lines
// must match the text at its position.
func applyPatch(t *testing.T, original, patch string) string {
	t.Helper()
	oldLines := strings.SplitAfter(original, "\n")
	if oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
	}
	var out []string
	next := 0 // index into oldLines of the first line not yet copied
	hunkHeader := regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+\d+(?:,\d+)? @@`)
	patchLines := strings.SplitAfter(patch, "\n")
	for i := 0; i < len(patchLines); i++ {
		header := hunkHeader.FindStringSubmatch(patchLines[i])
		if header == nil {
			continue
		}
		start := must(strconv.Atoi(header[1])) - 1
		if header[2] == "0" {
			start++
		}
		out = append(out, oldLines[next:start]...)
		next = start
		for i+1 < len(patchLines) && patchLines[i+1] != "" && !strings.HasPrefix(patchLines[i+1], "@@") {
			i++
			line := patchLines[i]
			switch line[0] {
			case ' ', '-':
				if next >= len(oldLines) || oldLines[next] != line[1:] {
					t.Fatalf("patch line %q does not match the original at line %d", line, next+1)
				}
				if line[0] == ' ' {
					out = append(out, oldLines[next])
				}
				next++
			case '+':
				out = append(out, line[1:])
			}
		}
	}
	out = append(out, oldLines[next:]...)
	return strings.Join(out, "")
}
