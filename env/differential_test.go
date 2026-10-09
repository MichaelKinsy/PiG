package env

// pi: packages/env/src/remote-env.ts

// Ports packages/env/test/differential.test.ts

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// pair is a NodeExecutionEnv and a RemoteExecutionEnv over the same machine, each in its own root.
func pair(t *testing.T, connection *Connection) (local, remote durableenv.ExecutionEnv, roots [2]string) {
	t.Helper()
	localRoot, remoteRoot := t.TempDir(), t.TempDir()
	return node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Cwd: localRoot}),
		NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: remoteRoot}),
		[2]string{localRoot, remoteRoot}
}

// normalize is a result with the environment's root replaced and only the error code of failures, for comparison.
func normalize(value any, root string) any {
	var walk func(value any) any
	walk = func(value any) any {
		switch typed := value.(type) {
		case nil, bool, string, int, int64, float64:
			return typed
		case []byte:
			return map[string]any{"bytes": hex.EncodeToString(typed)}
		case durableenv.FileInfo:
			return map[string]any{"name": typed.Name, "path": typed.Path, "kind": string(typed.Kind), "size": typed.Size, "mtimeMs": "mtime"}
		case durableenv.LineScan:
			return typed
		case durableenv.DirPage:
			entries := make([]any, len(typed.Entries))
			for index, entry := range typed.Entries {
				entries[index] = walk(entry)
			}
			return map[string]any{"entries": entries, "done": typed.Done}
		case []durableenv.FileInfo:
			entries := make([]any, len(typed))
			for index, entry := range typed {
				entries[index] = walk(entry)
			}
			return entries
		case []string:
			return typed
		case outcome:
			result := map[string]any{"ok": typed.err == nil}
			if typed.err != nil {
				result["code"] = errorCodeOf(typed.err)
				return result
			}
			result["value"] = walk(typed.value)
			return result
		case []any:
			items := make([]any, len(typed))
			for index, item := range typed {
				items[index] = walk(item)
			}
			return items
		case map[string]any:
			result := map[string]any{}
			for key, item := range typed {
				result[key] = walk(item)
			}
			return result
		}
		return fmt.Sprintf("%v", value)
	}
	encoded, err := json.Marshal(walk(value))
	if err != nil {
		panic(err)
	}
	text := string(encoded)
	// Roots appear JSON-escaped, and resolved paths spell them without Windows' 8.3 short names (RUNNER~1).
	escaped := func(path string) string {
		quoted, _ := json.Marshal(path)
		return string(quoted[1 : len(quoted)-1])
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		text = strings.ReplaceAll(text, escaped(real), "<root>")
	}
	text = strings.ReplaceAll(text, escaped(root), "<root>")
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		panic(err)
	}
	return decoded
}

// outcome is a value or the error of an operation, so a failure compares by its code.
type outcome struct {
	value any
	err   error
}

func result[T any](value T, err error) outcome { return outcome{value: value, err: err} }

func failure(err error) outcome { return outcome{err: err} }

var names = []string{"a.txt", "b", "dir", "dir/c.txt", "dir/sub", "dir/sub/d.bin", "missing", "a.txt/x"}

type content struct {
	text  string
	bytes []byte
}

func (c content) value() any {
	if c.bytes != nil {
		return c.bytes
	}
	return c.text
}

func (c content) String() string {
	if c.bytes != nil {
		return "<bytes>"
	}
	if len(c.text) > 20 {
		return fmt.Sprintf("<%d chars>", len(c.text))
	}
	return fmt.Sprintf("%q", c.text)
}

var contents = []content{
	{text: ""},
	{text: "hello\n"},
	{text: "\ufeffbom\r\nline"},
	{bytes: []byte{0xe2, 0x82, 0x0a, 0xff, 0xef, 0xbb, 0xbf}},
	{text: strings.Repeat("x", 70_000)},
	// Crosses write chunks, read chunks and multi-byte characters split between them.
	{text: strings.Repeat("é", 300_001)},
	// A byte-order mark stays when Node's readFile decodes a file of several reads.
	{text: "\ufeff" + strings.Repeat("é", 300_001)},
	// Invalid sequences cut by the 512 KiB read boundary, each replaced like decoding the whole file at once.
	{bytes: append(append(bytes.Repeat([]byte("x"), 512*1024-2), 0xe2, 0x82), append([]byte("a"), 0xff, 0xff, 0xf0, 0x9f)...)},
	{bytes: append(bytes.Repeat([]byte("y"), 512*1024-1), 0xf0, 0x9f, 0x98, 0x80, 0xe2)},
}

type operation func(env durableenv.ExecutionEnv) outcome

// randomOperation is a random operation with its arguments fixed, and a label for failure messages.
func randomOperation(next *rand.Rand) (string, operation) {
	name := func() string { return names[next.Intn(len(names))] }
	flag := func() bool { return next.Float64() < 0.5 }
	small := func(limit int) int { return next.Intn(limit) }
	data := func() content { return contents[next.Intn(len(contents))] }
	ctx := context.Background()
	type choice func() (string, operation)
	choices := []choice{
		func() (string, operation) {
			n, c := name(), data()
			return fmt.Sprintf("writeFile(%q, %s)", n, c), func(env durableenv.ExecutionEnv) outcome { return failure(env.WriteFile(ctx, n, c.value())) }
		},
		func() (string, operation) {
			n, c := name(), data()
			return fmt.Sprintf("appendFile(%q, %s)", n, c), func(env durableenv.ExecutionEnv) outcome { return failure(env.AppendFile(ctx, n, c.value())) }
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("readTextFile(%q)", n), func(env durableenv.ExecutionEnv) outcome { return result(env.ReadTextFile(ctx, n)) }
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("readBinaryFile(%q)", n), func(env durableenv.ExecutionEnv) outcome { return result(env.ReadBinaryFile(ctx, n)) }
		},
		func() (string, operation) {
			n, m := name(), small(4)
			return fmt.Sprintf("readTextLines(%q, %d)", n, m), func(env durableenv.ExecutionEnv) outcome {
				return result(env.ReadTextLines(ctx, n, &durableenv.ReadTextLinesOptions{MaxLines: &m}))
			}
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("fileInfo(%q)", n), func(env durableenv.ExecutionEnv) outcome { return result(env.FileInfo(ctx, n)) }
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("exists(%q)", n), func(env durableenv.ExecutionEnv) outcome { return result(env.Exists(ctx, n)) }
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("listDir(%q)", n), func(env durableenv.ExecutionEnv) outcome { return result(env.ListDir(ctx, n)) }
		},
		func() (string, operation) {
			n, r := name(), flag()
			// upstream: packages/env/src/remote-env.ts:750 createDir (recursive option), compared with NodeExecutionEnv.
			return fmt.Sprintf("createDir(%q, %v)", n, r), func(env durableenv.ExecutionEnv) outcome {
				return failure(env.CreateDir(ctx, n, &durableenv.CreateDirOptions{Recursive: &r}))
			}
		},
		func() (string, operation) {
			n, r, f := name(), flag(), flag()
			return fmt.Sprintf("remove(%q, %v, %v)", n, r, f), func(env durableenv.ExecutionEnv) outcome {
				return failure(env.Remove(ctx, n, &durableenv.RemoveOptions{Recursive: r, Force: f}))
			}
		},
		func() (string, operation) {
			a, b := name(), name()
			return fmt.Sprintf("renameFile(%q, %q)", a, b), func(env durableenv.ExecutionEnv) outcome { return failure(env.RenameFile(ctx, a, b)) }
		},
		func() (string, operation) {
			n, size := name(), small(10)
			return fmt.Sprintf("truncateFile(%q, %d)", n, size), func(env durableenv.ExecutionEnv) outcome {
				return failure(env.TruncateFile(ctx, n, int64(size)))
			}
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("flushFile(%q)", n), func(env durableenv.ExecutionEnv) outcome { return failure(env.FlushFile(ctx, n)) }
		},
		func() (string, operation) {
			n := name()
			return fmt.Sprintf("canonicalPath(%q)", n), func(env durableenv.ExecutionEnv) outcome { return result(env.CanonicalPath(ctx, n)) }
		},
		func() (string, operation) {
			n, noFollow, offset, length := name(), flag(), small(8), small(8)
			return fmt.Sprintf("openBinaryReader(%q, %v, %d, %d)", n, noFollow, offset, length), func(env durableenv.ExecutionEnv) outcome {
				reader, err := env.OpenBinaryReader(ctx, n, &durableenv.OpenBinaryReaderOptions{NoFollow: noFollow})
				if err != nil {
					return failure(err)
				}
				defer func() { _ = reader.Close(ctx) }()
				read := result(reader.Read(ctx, int64(offset), int64(length)))
				scan := result(reader.ScanLines(ctx, durableenv.ScanLinesOptions{StartLine: 0}))
				return outcome{value: []any{read, scan}}
			}
		},
	}
	label, run := choices[next.Intn(len(choices))]()
	return label, run
}

// Runs random operations against a NodeExecutionEnv and a RemoteExecutionEnv and compares the outcomes, createDir with its recursive
// option included (packages/env/src/remote-env.ts:750 createDir, packages/env/src/remote-env.ts:760 remove with its recursive and force options, packages/env/test/differential.test.ts).
func TestRemoteExecutionEnvAgainstNodeExecutionEnv(t *testing.T) {
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)

	t.Run("gives the same results for random file operation sequences", func(t *testing.T) {
		// upstream: packages/env/test/differential.test.ts:228
		for seed := int64(1); seed <= 60; seed++ {
			next := rand.New(rand.NewSource(seed))
			local, remote, roots := pair(t, connection)
			for step := range 25 {
				label, run := randomOperation(next)
				expected := normalize(run(local), roots[0])
				actual := normalize(run(remote), roots[1])
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("seed %d step %d %s:\nremote %v\nlocal  %v", seed, step, label, actual, expected)
				}
			}
		}
	})

	t.Run("reads every shape of file like Node", func(t *testing.T) {
		// Beyond upstream's random sequences, which reach the multi-read paths only by chance: every content is
		// written and read back whole, in lines and through a reader.
		local, remote, roots := pair(t, connection)
		ctx := context.Background()
		for index, c := range contents {
			name := fmt.Sprintf("file-%d", index)
			mustDo(local.WriteFile(ctx, name, c.value()))
			mustDo(remote.WriteFile(ctx, name, c.value()))
			results := func(env durableenv.ExecutionEnv, root string) any {
				reader, readerErr := env.OpenBinaryReader(ctx, name, nil)
				var read, scan outcome
				if readerErr == nil {
					read = result(reader.Read(ctx, 0, 1<<40-1))
					scan = result(reader.ScanLines(ctx, durableenv.ScanLinesOptions{StartLine: 0}))
					_ = reader.Close(ctx)
				}
				return normalize(map[string]any{
					"text":   result(env.ReadTextFile(ctx, name)),
					"binary": result(env.ReadBinaryFile(ctx, name)),
					"lines":  result(env.ReadTextLines(ctx, name, nil)),
					"read":   read,
					"scan":   scan,
					"info":   result(env.FileInfo(ctx, name)),
				}, root)
			}
			if actual, expected := results(remote, roots[1]), results(local, roots[0]); !reflect.DeepEqual(actual, expected) {
				t.Fatalf("content %d (%s):\nremote %.300v\nlocal  %.300v", index, c, actual, expected)
			}
		}
	})

	t.Run("reads devices, FIFOs and directories like Node", func(t *testing.T) {
		// upstream: packages/env/test/differential.test.ts:241
		if runtime.GOOS == "windows" {
			t.Skip("no FIFOs on Windows")
		}
		local, remote, roots := pair(t, connection)
		ctx := context.Background()
		results := func(env durableenv.ExecutionEnv, root string) any {
			fifo := filepath.Join(root, "fifo")
			made, err := env.Exec(ctx, []string{"mkfifo", fifo}, nil)
			if err != nil || made.ExitCode != 0 {
				t.Fatalf("mkfifo failed: %v", err)
			}
			mustDo(env.CreateDir(ctx, "dir", nil))
			// A writer for each blocking open of the FIFO.
			write := func(text string) {
				command := exec.Command("sh", "-c", `printf '%s' "$1" > "$2"`, "sh", text, fifo)
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
			}
			write("fifo text\n")
			fifoText := result(env.ReadTextFile(ctx, "fifo"))
			write("fifo bytes")
			fifoBytes := result(env.ReadBinaryFile(ctx, "fifo"))
			write("x")
			fifoLines := result(env.ReadTextLines(ctx, "fifo", nil))
			dirReader, dirReaderErr := env.OpenTextLineReader(ctx, "dir")
			var dirLine outcome
			if dirReaderErr == nil {
				line, err := dirReader.ReadLine(ctx)
				dirLine = result(line, err)
				_ = dirReader.Close(ctx)
			}
			lineText := func(value outcome) any {
				if value.err != nil {
					return value
				}
				if line, ok := value.value.(*durableenv.TextLine); ok && line != nil {
					return outcome{value: map[string]any{"text": line.Text, "terminated": line.Terminated}}
				}
				return outcome{value: nil}
			}
			return normalize(map[string]any{
				"nullText":        result(env.ReadTextFile(ctx, "/dev/null")),
				"nullBytes":       result(env.ReadBinaryFile(ctx, "/dev/null")),
				"nullLines":       result(env.ReadTextLines(ctx, "/dev/null", nil)),
				"fifoText":        fifoText,
				"fifoBytes":       fifoBytes,
				"fifoLines":       fifoLines,
				"dirReaderOpened": dirReaderErr == nil,
				"dirLine":         lineText(dirLine),
				"dirText":         result(env.ReadTextFile(ctx, "dir")),
				"dirBytes":        result(env.ReadBinaryFile(ctx, "dir")),
			}, root)
		}
		if actual, expected := results(remote, roots[1]), results(local, roots[0]); !reflect.DeepEqual(actual, expected) {
			t.Fatalf("remote %v\nlocal  %v", actual, expected)
		}
	})

	t.Run("lists names that are not UTF-8 like Node", func(t *testing.T) {
		// upstream: packages/env/test/differential.test.ts:283
		// macOS file systems refuse names that are not UTF-8.
		if runtime.GOOS != "linux" && runtime.GOOS != "android" {
			t.Skip("only Linux file systems keep names that are not UTF-8")
		}
		_, remote, roots := pair(t, connection)
		for _, name := range [][]byte{{0x62}, {0x61, 0xff}, {0xc3, 0xa9}, {0x61, 0xfe, 0x7a}, {0x7a}} {
			mustDo(os.WriteFile(roots[1]+"/"+string(name), []byte("x"), 0o600))
		}
		ctx := context.Background()
		// Node decodes the names with replacement characters and lstats them under that name, so listDir fails with
		// ENOENT for them and a directory reader skips them. (The local NodeExecutionEnv of PiG lists the raw names, so
		// this case states Node's results instead of comparing with it.)
		if _, err := remote.ListDir(ctx, "."); errorCodeOf(err) != "not_found" {
			t.Fatalf("listDir code %s, want not_found", errorCodeOf(err))
		}
		reader := must(remote.OpenDirReader(ctx, "."))
		defer func() { _ = reader.Close(ctx) }()
		var listed []string
		for pages := 0; ; pages++ {
			page := must(reader.Next(ctx, 2))
			if len(page.Entries) > 2 {
				t.Fatalf("page of %d entries, want at most 2", len(page.Entries))
			}
			for _, entry := range page.Entries {
				listed = append(listed, entry.Name)
			}
			if page.Done {
				break
			}
			if pages > 10 {
				t.Fatal("the reader never reported the end")
			}
		}
		slices.Sort(listed)
		if want := []string{"b", "z", "é"}; !slices.Equal(listed, want) {
			t.Fatalf("listed %q, want %q", listed, want)
		}
	})

	t.Run("gives the same read and bash tool results", func(t *testing.T) {
		// upstream: packages/env/test/differential.test.ts:309
		local, remote, roots := pair(t, connection)
		ctx := context.Background()
		files := []struct {
			path string
			data any
		}{
			{"text.txt", "one\ntwo\n\nthree"},
			{"big.txt", strings.Repeat("line\n", 3000)},
			{"long.txt", strings.Repeat("é", 40_000) + "\nend"},
			{"bom.txt", "\ufeffhello"},
			{"bad.txt", []byte{0x61, 0xe2, 0x82, 0x0a, 0x62}},
		}
		for _, file := range files {
			mustDo(local.WriteFile(ctx, file.path, file.data))
			mustDo(remote.WriteFile(ctx, file.path, file.data))
		}
		run := func(env durableenv.ExecutionEnv, tool *durable.ToolRegistration, args map[string]any) any {
			api := &toolAPI{env: env}
			toolResult, err := tool.Execute(ctx, args, api)
			output := strings.Join(api.output(), "")
			if err != nil {
				return map[string]any{"error": err.Error(), "output": output}
			}
			encoded, marshalErr := json.Marshal(toolResult)
			if marshalErr != nil {
				panic(marshalErr)
			}
			var decoded any
			mustDo(json.Unmarshal(encoded, &decoded))
			return map[string]any{"result": decoded, "output": output}
		}
		cases := []struct {
			tool *durable.ToolRegistration
			args map[string]any
		}{
			{tools.CreateReadTool(), map[string]any{"path": "text.txt"}},
			{tools.CreateReadTool(), map[string]any{"path": "text.txt", "offset": 2.0, "limit": 1.0}},
			{tools.CreateReadTool(), map[string]any{"path": "big.txt"}},
			{tools.CreateReadTool(), map[string]any{"path": "long.txt"}},
			{tools.CreateReadTool(), map[string]any{"path": "bom.txt"}},
			{tools.CreateReadTool(), map[string]any{"path": "bad.txt"}},
			{tools.CreateReadTool(), map[string]any{"path": "missing.txt"}},
			{tools.CreateBashTool(nil), map[string]any{"command": "cat text.txt; echo err >&2; exit 0"}},
			{tools.CreateBashTool(nil), map[string]any{"command": "exit 3"}},
			{tools.CreateBashTool(nil), map[string]any{"command": "printf 'a\\nb'; ls"}},
		}
		for _, c := range cases {
			expected := normalize(run(local, c.tool, c.args), roots[0])
			actual := normalize(run(remote, c.tool, c.args), roots[1])
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("%v:\nremote %v\nlocal  %v", c.args, actual, expected)
			}
		}
	})
}

// toolAPI is a minimal execution API: the environment and collected output. Calling any other operation panics on the
// embedded nil interface.
type toolAPI struct {
	durable.ToolExecutionApi
	env durableenv.ExecutionEnv
	mu  sync.Mutex
	out []string
}

func (api *toolAPI) Env() durableenv.ExecutionEnv                     { return api.env }
func (api *toolAPI) OutputWindow() *durableenv.ShellOutputWindow      { return nil }
func (api *toolAPI) Diagnostic(durable.ToolDiagnostic)                {}
func (api *toolAPI) Details(context.Context, durable.JsonValue) error { return nil }

func (api *toolAPI) Output(chunk any, _ ...durableenv.ShellOutputSkip) {
	api.mu.Lock()
	defer api.mu.Unlock()
	switch typed := chunk.(type) {
	case string:
		api.out = append(api.out, typed)
	case []byte:
		api.out = append(api.out, string(typed))
	}
}

func (api *toolAPI) output() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]string(nil), api.out...)
}
