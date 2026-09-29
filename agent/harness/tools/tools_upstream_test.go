package tools

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/ai"
	builtin "github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type testInvocation struct{}

func (testInvocation) InvocationID() string                                   { return "test-result" }
func (testInvocation) OperationID() string                                    { return "test-operation" }
func (testInvocation) TurnID() string                                         { return "test-turn" }
func (testInvocation) GetMemo(context.Context, string) (any, bool, error)     { return nil, false, nil }
func (testInvocation) SetMemo(context.Context, string, *any) error            { return nil }
func noUpdate(harness.AgentToolResult, harness.AgentHarnessToolUpdateOptions) {}
func textOutput(result harness.AgentToolResult) string {
	var texts []string
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}
func newEnv(t *testing.T) *envpkg.NodeExecutionEnv {
	t.Helper()
	env := envpkg.NewNodeExecutionEnv(envpkg.NodeExecutionEnvOptions{Cwd: t.TempDir()})
	t.Cleanup(func() { env.Cleanup(context.Background()) })
	return env
}
func writeFile(t *testing.T, env harness.ExecutionEnv, path, content string) {
	t.Helper()
	if err := env.WriteFile(t.Context(), path, []byte(content)); err != nil {
		t.Fatal(err)
	}
}
func readFile(t *testing.T, env harness.ExecutionEnv, path string) string {
	t.Helper()
	content, err := env.ReadTextFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
func execute(t *testing.T, tool *harness.AgentHarnessTool, env harness.ExecutionEnv, id string, params map[string]any) harness.AgentToolResult {
	t.Helper()
	result, err := tool.Execute(t.Context(), id, params, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func lines(n int, format string) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(format, i+1)
	}
	return strings.Join(rows, "\n")
}
func edits(path string, pairs ...string) map[string]any {
	items := make([]any, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		items = append(items, map[string]any{"oldText": pairs[i], "newText": pairs[i+1]})
	}
	return map[string]any{"path": path, "edits": items}
}

func TestHarnessRead(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:189
	for _, signature := range []string{"GIF87a", "GIF89a"} {
		t.Run("detects the complete "+signature+" signature", func(t *testing.T) {
			if got := builtin.SupportedImageMime([]byte(signature)); got != "image/gif" {
				t.Fatalf("mime=%q", got)
			}
		})
	}
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:193
	t.Run("reads text with offsets, limits, and continuation notices", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "test.txt", lines(100, "Line %d"))
		out := textOutput(execute(t, CreateReadTool(nil), env, "read-1", map[string]any{"path": "test.txt", "offset": 41, "limit": 20}))
		for _, text := range []string{"Line 41", "Line 60", "[40 more lines in file. Use offset=61 to continue.]"} {
			if !strings.Contains(out, text) {
				t.Fatalf("output lacks %q: %q", text, out)
			}
		}
		for _, text := range []string{"Line 40", "Line 61"} {
			if strings.Contains(out, text) {
				t.Fatalf("output contains %q", text)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:220
	t.Run("truncates large text by line count", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "large.txt", lines(2500, "Line %d"))
		result := execute(t, CreateReadTool(nil), env, "read-2", map[string]any{"path": "large.txt"})
		if !strings.Contains(textOutput(result), "[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]") {
			t.Fatal(textOutput(result))
		}
		tr := result.Details.(*ReadToolDetails).Truncation
		if !tr.Truncated || tr.TruncatedBy == nil || *tr.TruncatedBy != "lines" || tr.TotalLines != 2500 || tr.OutputLines != 2000 {
			t.Fatalf("truncation=%+v", tr)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:248
	t.Run("does not count a trailing newline as an extra line at the truncation limit", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "exact.txt", strings.Repeat("x\n", 2000))
		result := execute(t, CreateReadTool(nil), env, "read-exact", map[string]any{"path": "exact.txt"})
		if result.Details != nil || strings.Contains(textOutput(result), "Use offset=") {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:271
	t.Run("rejects offsets beyond the file", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "short.txt", "one\ntwo\nthree")
		_, err := CreateReadTool(nil).Execute(t.Context(), "read-3", map[string]any{"path": "short.txt", "offset": 100}, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
		if err == nil || !strings.Contains(err.Error(), "Offset 100 is beyond end of file (3 lines total)") {
			t.Fatalf("error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:287
	t.Run("detects supported images by content", func(t *testing.T) {
		env := newEnv(t)
		const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg=="
		png, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.WriteFile(t.Context(), "image.txt", png); err != nil {
			t.Fatal(err)
		}
		result := execute(t, CreateReadTool(nil), env, "read-4", map[string]any{"path": "image.txt"})
		if !strings.Contains(textOutput(result), "Read image file [image/png]") || !slices.ContainsFunc(result.Content, func(block ai.ToolResultMessageContent) bool {
			image, ok := block.(ai.ImageContent)
			return ok && image.Data == encoded && image.MimeType == "image/png"
		}) {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:314
	t.Run("delegates image conversion and resizing to an injected processor", func(t *testing.T) {
		env := newEnv(t)
		bmp := tinyBMP()
		if err := env.WriteFile(t.Context(), "image.bmp", bmp); err != nil {
			t.Fatal(err)
		}
		called := false
		tool := CreateReadTool(&ReadToolOptions{AutoResizeImages: new(false), ImageProcessor: func(_ context.Context, data []byte, mime string, resize bool) (ReadImageProcessorResult, error) {
			called = true
			if mime != "image/bmp" || resize || !slices.Equal(data, bmp) {
				t.Errorf("processor input mime=%s resize=%v bytes=%v", mime, resize, data)
			}
			return ReadImageProcessorResult{OK: true, Data: "converted", MimeType: "image/png", Hints: []string{"[Image converted from image/bmp to image/png.]"}}, nil
		}})
		result := execute(t, tool, env, "read-bmp", map[string]any{"path": "image.bmp"})
		if !called || !strings.Contains(textOutput(result), "[Image converted from image/bmp to image/png.]") || !slices.ContainsFunc(result.Content, func(block ai.ToolResultMessageContent) bool {
			image, ok := block.(ai.ImageContent)
			return ok && image.Data == "converted" && image.MimeType == "image/png"
		}) {
			t.Fatalf("result=%+v called=%v", result, called)
		}
	})
}
func tinyBMP() []byte {
	data := make([]byte, 58)
	copy(data, "BM")
	binary.LittleEndian.PutUint32(data[2:], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[10:], 54)
	binary.LittleEndian.PutUint32(data[14:], 40)
	binary.LittleEndian.PutUint32(data[18:], 1)
	binary.LittleEndian.PutUint32(data[22:], 1)
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 24)
	binary.LittleEndian.PutUint32(data[34:], 4)
	return data
}

type blockingMutationEnv struct {
	*envpkg.NodeExecutionEnv
	firstStarted, release       chan struct{}
	edit                        bool
	secondStarted, firstSettled atomic.Bool
}

func (env *blockingMutationEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	if string(content) == "first\n" || string(content) == "ALPHA\nbeta\n" {
		close(env.firstStarted)
		<-env.release
		if env.edit {
			err := env.NodeExecutionEnv.WriteFile(context.Background(), path, content)
			env.firstSettled.Store(true)
			return err
		}
	}
	if string(content) == "second\n" || string(content) == "ALPHA\nBETA\n" || string(content) == "alpha\nBETA\n" {
		env.secondStarted.Store(true)
	}
	return env.NodeExecutionEnv.WriteFile(ctx, path, content)
}

// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:349
func TestHarnessWriteCreatesParentDirectories(t *testing.T) {
	env := newEnv(t)
	result := execute(t, CreateWriteTool(), env, "write-1", map[string]any{"path": "nested/dir/file.txt", "content": "hello"})
	if textOutput(result) != "Successfully wrote to nested/dir/file.txt" || readFile(t, env, "nested/dir/file.txt") != "hello" {
		t.Fatalf("result=%+v", result)
	}
}

// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:364
func TestHarnessWriteKeepsMutationQueueLockedUntilAbortedWriteSettles(t *testing.T) {
	testAbortedMutation(t, false)
}

// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:478
func TestHarnessEditKeepsMutationQueueLockedUntilAbortedWriteSettles(t *testing.T) {
	testAbortedMutation(t, true)
}
func testAbortedMutation(t *testing.T, edit bool) []any {
	t.Helper()
	var observed []any
	synctest.Test(t, func(t *testing.T) {
		env := &blockingMutationEnv{NodeExecutionEnv: newEnv(t), firstStarted: make(chan struct{}), release: make(chan struct{}), edit: edit}
		tool := CreateWriteTool()
		first := map[string]any{"path": "file.txt", "content": "first\n"}
		second := map[string]any{"path": "file.txt", "content": "second\n"}
		want := "second\n"
		if edit {
			writeFile(t, env, "file.txt", "alpha\nbeta\n")
			tool = CreateEditTool()
			first = edits("file.txt", "alpha", "ALPHA")
			second = edits("file.txt", "beta", "BETA")
			want = "ALPHA\nBETA\n"
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		firstDone, secondDone := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := tool.Execute(ctx, "first", first, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
			firstDone <- err
		}()
		<-env.firstStarted
		cancel()
		go func() {
			_, err := tool.Execute(t.Context(), "second", second, noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
			secondDone <- err
		}()
		synctest.Wait()
		secondBeforeSettlement := env.secondStarted.Load()
		if secondBeforeSettlement {
			t.Error("second mutation started before aborted first write settled")
		}
		close(env.release)
		if err := <-firstDone; err == nil || edit && !strings.Contains(err.Error(), "Operation aborted") {
			t.Errorf("first error=%v", err)
		}
		if err := <-secondDone; err != nil {
			t.Fatal(err)
		}
		if edit && !env.firstSettled.Load() {
			t.Fatal("first write did not settle")
		}
		content := readFile(t, env, "file.txt")
		if content != want {
			t.Fatalf("file=%q want=%q", content, want)
		}
		observed = []any{edit, secondBeforeSettlement, env.firstSettled.Load(), content}
	})
	return observed
}

func TestHarnessEdit(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:399
	t.Run("applies disjoint edits and returns both diff formats", func(t *testing.T) {
		env := newEnv(t)
		original := "alpha\nbeta\ngamma\ndelta\n"
		want := "ALPHA\nbeta\nGAMMA\ndelta\n"
		writeFile(t, env, "edit.txt", original)
		result := execute(t, CreateEditTool(), env, "edit-1", edits("edit.txt", "alpha\n", "ALPHA\n", "gamma\n", "GAMMA\n"))
		if textOutput(result) != "Successfully replaced 2 block(s) in edit.txt." {
			t.Fatal(textOutput(result))
		}
		details := result.Details.(*EditToolDetails)
		if !strings.Contains(details.Diff, "ALPHA") || !strings.Contains(details.Diff, "GAMMA") || applyPatch(t, original, details.Patch) != want || readFile(t, env, "edit.txt") != want {
			t.Fatalf("details=%+v", details)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:428
	t.Run("matches all edits against the original and rejects overlaps", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "edit.txt", "one\ntwo\nthree\n")
		_, err := CreateEditTool().Execute(t.Context(), "edit-2", edits("edit.txt", "one\ntwo\n", "ONE\nTWO\n", "two\nthree\n", "TWO\nTHREE\n"), noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
		if err == nil || !strings.Contains(err.Error(), "overlap") || readFile(t, env, "edit.txt") != "one\ntwo\nthree\n" {
			t.Fatalf("error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:451
	t.Run("rejects missing and duplicate target text", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "edit.txt", "foo foo foo")
		for _, tc := range []struct{ old, new, want string }{{"bar", "baz", "Could not find the exact text"}, {"foo", "bar", "Found 3 occurrences"}} {
			_, err := CreateEditTool().Execute(t.Context(), "edit", edits("edit.txt", tc.old, tc.new), noUpdate, ExecutionToolContext{Env: env}, testInvocation{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:539
	t.Run("edits regular files through symlinks", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "target.txt", "before\n")
		testenv.Symlink(t, "target.txt", filepath.Join(env.Cwd(), "link.txt"))
		execute(t, CreateEditTool(), env, "edit-symlink", edits("link.txt", "before", "after"))
		if got := readFile(t, env, "target.txt"); got != "after\n" {
			t.Fatalf("file=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:556
	t.Run("preserves BOM and CRLF line endings", func(t *testing.T) {
		env := newEnv(t)
		writeFile(t, env, "edit.txt", "\ufeffone\r\ntwo\r\n")
		execute(t, CreateEditTool(), env, "edit-5", edits("edit.txt", "two", "TWO"))
		if got := readFile(t, env, "edit.txt"); got != "\ufeffone\r\nTWO\r\n" {
			t.Fatalf("file=%q", got)
		}
	})
}

type slowReadEnv struct{ *envpkg.NodeExecutionEnv }

func (env *slowReadEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	time.Sleep(20 * time.Millisecond)
	return env.NodeExecutionEnv.ReadTextFile(ctx, path)
}

// .upstream/v0.87.1/packages/agent/test/harness/tools.test.ts:511
func TestHarnessEditSerializesCanonicalAndSymlinkPaths(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		env := &slowReadEnv{newEnv(t)}
		writeFile(t, env, "target.txt", "alpha\nbeta\ngamma\n")
		testenv.Symlink(t, "target.txt", filepath.Join(env.Cwd(), "link.txt"))
		tool := CreateEditTool()
		var wg sync.WaitGroup
		for _, params := range []map[string]any{edits("target.txt", "alpha", "ALPHA"), edits("link.txt", "beta", "BETA")} {
			wg.Go(func() { execute(t, tool, env, "edit", params) })
		}
		wg.Wait()
		if got := readFile(t, env, "target.txt"); got != "ALPHA\nBETA\ngamma\n" {
			t.Fatalf("file=%q", got)
		}
	})
}

// applyPatch independently applies unified hunks and validates every consumed
// context/deletion line, as upstream diff.applyPatch does for this fixture.
func applyPatch(t *testing.T, original, patch string) string {
	t.Helper()
	source := strings.SplitAfter(original, "\n")
	at := 0
	var out strings.Builder
	inHunk := false
	for _, line := range strings.SplitAfter(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			var start, count int
			if _, err := fmt.Sscanf(line, "@@ -%d,%d", &start, &count); err != nil {
				t.Fatal(err)
			}
			for at < start-1 {
				out.WriteString(source[at])
				at++
			}
			inHunk = true
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		switch line[0] {
		case ' ', '-':
			if at >= len(source) || source[at] != line[1:] {
				t.Fatalf("invalid patch context %q at %d: %q", line, at, patch)
			}
			if line[0] == ' ' {
				out.WriteString(line[1:])
			}
			at++
		case '+':
			out.WriteString(line[1:])
		default:
			t.Fatalf("unexpected patch line %q", line)
		}
	}
	for ; at < len(source); at++ {
		out.WriteString(source[at])
	}
	return out.String()
}
