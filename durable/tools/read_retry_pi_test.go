package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// shiftingEnv wraps an execution environment so that the reader it opens reports scripted metadata: shift receives the 0-based number of the Info call and the real info.
type shiftingEnv struct {
	env.ExecutionEnv
	shift func(call int, info env.FileInfo) env.FileInfo
	calls *int
}

func (e shiftingEnv) OpenBinaryReader(ctx context.Context, path string, options *env.OpenBinaryReaderOptions) (env.BinaryReader, error) {
	reader, err := e.ExecutionEnv.OpenBinaryReader(ctx, path, options)
	if err != nil {
		return nil, err
	}
	return shiftingReader{BinaryReader: reader, shift: e.shift, calls: e.calls}, nil
}

type shiftingReader struct {
	env.BinaryReader
	shift func(call int, info env.FileInfo) env.FileInfo
	calls *int
}

func (r shiftingReader) Info(ctx context.Context) (env.FileInfo, error) {
	info, err := r.BinaryReader.Info(ctx)
	if err != nil {
		return info, err
	}
	call := *r.calls
	*r.calls++
	return r.shift(call, info), nil
}

// Pi packages/durable/src/tools/read.ts (:84-96): the file's metadata is read before and after the read; a file that grew, or kept its size and modification time, is returned as read;
// a file that shrank or was rewritten in place is read again once, and a second change throws `<path> changed while it was read`.
// mutation-checked: `attempt != 1` for `attempt == 1` fails the retry cases, and a retry that never stops fails the always-changing case.
func TestReadReadsAChangedFileAgainOnceAndThenFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		shift     func(call int, info env.FileInfo) env.FileInfo
		wantCalls int
		wantErr   string
	}{
		{"unchanged", func(_ int, info env.FileInfo) env.FileInfo { return info }, 2, ""},
		{"grown", func(call int, info env.FileInfo) env.FileInfo {
			if call == 1 {
				info.Size++
			}
			return info
		}, 2, ""},
		{"same size, new modification time, then stable", func(call int, info env.FileInfo) env.FileInfo {
			if call == 1 {
				info.MtimeMs++
			}
			return info
		}, 4, ""},
		{"shrunk once, then stable", func(call int, info env.FileInfo) env.FileInfo {
			if call == 1 {
				info.Size--
			}
			return info
		}, 4, ""},
		{"changed on both attempts", func(call int, info env.FileInfo) env.FileInfo {
			if call%2 == 1 {
				info.MtimeMs++
			}
			return info
		}, 4, "notes.txt changed while it was read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := createEnv(t)
			if err := os.WriteFile(filepath.Join(base.Cwd(), "notes.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, err := run(CreateReadTool(nil), map[string]any{"path": "notes.txt"}, shiftingEnv{ExecutionEnv: base, shift: tc.shift, calls: &calls}, background)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else {
				mustDo(t, err)
				if !strings.Contains(textOutput(result.ToolExecutionResult), "one") {
					t.Errorf("result = %v", result.Output)
				}
			}
			if calls != tc.wantCalls {
				t.Errorf("Info was called %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}
