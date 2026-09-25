package subprocess

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func writeRawFrame(conn net.Conn, body string) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	_, err := conn.Write(append(header[:], body...))
	return err
}

func readRawFrame(conn net.Conn) (map[string]json.RawMessage, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(body, &frame); err != nil {
		return nil, err
	}
	return frame, nil
}

// rawRegisterServe returns an in-process extension that sends register as the
// given raw JSON, reports the first frame the host answers with, and reports
// the type of every frame it reads until the host closes the connection.
func rawRegisterServe(register string, first chan<- map[string]json.RawMessage, types chan<- []string) func(net.Conn) error {
	return func(conn net.Conn) error {
		var seen []string
		defer func() { types <- seen }()
		if err := writeRawFrame(conn, `{"type":"register","register":`+register+`}`); err != nil {
			return err
		}
		for {
			frame, err := readRawFrame(conn)
			if err != nil {
				return nil
			}
			var frameType string
			_ = json.Unmarshal(frame["type"], &frameType)
			if len(seen) == 0 {
				first <- frame
			}
			seen = append(seen, frameType)
		}
	}
}

// copySDKSource copies the SDK source files under src into dst the way pig
// stages them (no tests, caches, or the embedding bundle.go), passing each file
// through edit: the SDK another pig would have staged or built with.
func copySDKSource(t *testing.T, src, dst string, edit func(rel string, data []byte) []byte) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "__pycache__", "target", "tests":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "bundle.go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), edit(filepath.ToSlash(rel), data), 0o644)
	})
	if err != nil {
		t.Fatalf("copy SDK %s: %v", src, err)
	}
}

func reloadIssues(t *testing.T, h *Host, configs []ExtConfig) []string {
	t.Helper()
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rep := h.LastReloadReport()
	if rep == nil {
		t.Fatal("no reload report")
	}
	return rep.Issues
}

func requireOneIssueContaining(t *testing.T, issues []string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if len(issues) != 1 || !strings.Contains(issues[0], want) {
			t.Fatalf("reload issues = %q, want one containing %q", issues, want)
		}
	}
	t.Logf("reload issue: %s", issues[0])
}

// A registration this host cannot decode fails the load with the decode error,
// which names the field, and with how to rebuild the runner, instead of only
// saying that the connection closed before register.
func TestRegisterDecodeFailureNamesTheFieldAndTheRebuild(t *testing.T) {
	cases := []struct {
		name   string
		source string
		hint   string
	}{
		{"a runner pig builds from source", "/extensions/strict", "if pig or its SDK changed since this extension was built, run `pig reload` to rebuild it"},
		{"a prebuilt binary", "", `"strict" ships a prebuilt binary; if you updated pig, rebuild it against the current pig SDK`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			first := make(chan map[string]json.RawMessage, 1)
			types := make(chan []string, 1)
			register := `{"name":"strict","not_a_register_field":1}`
			_, err := h.LoadInProcess(testbudget.Context(t), ExtConfig{Name: "strict", Enabled: true, Source: tc.source}, rawRegisterServe(register, first, types))
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || loadErr.Code != "register_failed" {
				t.Fatalf("load error = %v, want register_failed", err)
			}
			if !strings.Contains(err.Error(), `unknown field "not_a_register_field"`) {
				t.Fatalf("load error = %v, want it to name the unknown register field", err)
			}
			if loadErr.Hint != tc.hint {
				t.Fatalf("load error hint = %q, want %q", loadErr.Hint, tc.hint)
			}
		})
	}
}

// A packed runner that imports an SDK whose registration differs from this
// pig's fails with the field it sent and `pig reload`, which restages this
// pig's SDKs so the runner is rebuilt against them.
func TestPackedRunnerWithAnotherSDKsRegisterShapeNamesTheRebuild(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not found: %v", err)
	}
	sdkRoot := filepath.Join(t.TempDir(), "sdk-py")
	copySDKSource(t, filepath.Join(findModuleRoot(t), "extensions", "sdk-py"), sdkRoot, func(rel string, data []byte) []byte {
		if rel != "pig_sdk/__init__.py" {
			return data
		}
		if bytes.Count(data, []byte(`"register": {`)) != 1 {
			t.Fatalf("Python SDK register payload moved")
		}
		return bytes.Replace(data, []byte(`"register": {`), []byte(`"register": {"field_from_another_sdk": True, `), 1)
	})
	t.Setenv("PIG_SDK_PY_ROOT", sdkRoot)
	root := writePackedPythonFactoryModule(t, "shapeskew_py", "shapeskew-py", "skew_tool")

	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	issues := reloadIssues(t, h, []ExtConfig{packedPythonFactoryConfig("shapeskew-py", root, "shapeskew_py", "shapeskew-py")})
	requireOneIssueContaining(t, issues,
		`unknown field "field_from_another_sdk"`,
		"if pig or its SDK changed since this extension was built, run `pig reload` to rebuild it")
}
