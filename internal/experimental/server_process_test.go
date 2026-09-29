package experimental

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// upstream: packages/coding-agent/src/experimental/server.ts:731-754. JSON parse failures retain a cause; valid JSON with an invalid schema has only the fixed diagnostic.
func TestParseServerModelOptions(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name  string
		input *string
		want  *SessionWorkerModel
	}{
		{name: "omitted"},
		{name: "model", input: new(`{"model":"custom/model:high"}`), want: &SessionWorkerModel{Model: "custom/model:high"}},
		{name: "provider and model", input: new(`{"provider":"custom","model":"model"}`), want: &SessionWorkerModel{Provider: new("custom"), Model: "model"}},
		{name: "whitespace is nonempty", input: new(`{"provider":" ","model":" "}`), want: &SessionWorkerModel{Provider: new(" "), Model: " "}},
		{name: "last duplicate wins", input: new(`{"model":"old","model":"new"}`), want: &SessionWorkerModel{Model: "new"}},
		{name: "lone UTF-16 units", input: new(`{"provider":"p\ud800","model":"m\udfff"}`), want: &SessionWorkerModel{Provider: new("p\xed\xa0\x80"), Model: "m\xed\xbf\xbf"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := parseServerModelOptions(row.input)
			if err != nil || !reflect.DeepEqual(got, row.want) {
				t.Fatalf("model options=%+v error=%v, want %+v", got, err, row.want)
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `1`, `1e999`, `true`, `"model"`, `{}`, `{"provider":"custom"}`, `{"model":""}`, `{"model":null}`, `{"model":1e999}`, `{"model":"x","provider":""}`, `{"model":"x","provider":null}`, `{"model":"x","provider":false}`, `{"model":"x","unknown":1}`, `{"model":"x","__proto__":{}}`} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseServerModelOptions(&raw)
			if got != nil || err == nil || err.Error() != "Internal server received invalid model options" || errors.Unwrap(err) != nil {
				t.Fatalf("model options=%+v error=%v, want fixed schema diagnostic without cause", got, err)
			}
		})
	}
	for _, raw := range []string{"", "{", `{"model":}`} {
		t.Run("malformed:"+raw, func(t *testing.T) {
			got, err := parseServerModelOptions(&raw)
			var syntax *json.SyntaxError
			if got != nil || err == nil || err.Error() != "Internal server received invalid model options" || !errors.As(err, &syntax) {
				t.Fatalf("model options=%+v error=%v, want fixed diagnostic with parser cause", got, err)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:757-764. Argument validation and model JSON validation precede startup and every filesystem effect.
func TestRunServerProcessValidatesBeforeStartup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "server-not-created")
	sessions := filepath.Join(root, "sessions-not-created")
	id := "00000000-0000-4000-8000-000000000001"
	for _, row := range []struct {
		name string
		args []string
		want string
	}{
		{name: "extra arguments first", args: []string{"relative", "bad", "relative", "{", "extra"}, want: "Internal server received unexpected arguments"},
		{name: "missing directory", want: "Internal server requires an absolute server directory"},
		{name: "empty directory", args: []string{""}, want: "Internal server requires an absolute server directory"},
		{name: "relative directory", args: []string{"relative"}, want: "Internal server requires an absolute server directory"},
		{name: "missing ID", args: []string{directory}, want: "Internal server requires a canonical server ID"},
		{name: "invalid ID", args: []string{directory, "bad"}, want: "Internal server requires a canonical server ID"},
		{name: "missing Session directory", args: []string{directory, id}, want: "Internal server requires an absolute Session directory"},
		{name: "relative Session directory", args: []string{directory, id, "relative"}, want: "Internal server requires an absolute Session directory"},
		{name: "empty Session directory", args: []string{directory, id, ""}, want: "Internal server requires an absolute Session directory"},
		{name: "malformed model options", args: []string{directory, id, sessions, "{"}, want: "Internal server received invalid model options"},
		{name: "invalid model schema", args: []string{directory, id, sessions, `{"provider":"custom"}`}, want: "Internal server received invalid model options"},
	} {
		t.Run(row.name, func(t *testing.T) {
			if err := RunServerProcess(t.Context(), row.args); err == nil || err.Error() != row.want {
				t.Fatalf("process error=%v, want %q", err, row.want)
			}
			for _, path := range []string{directory, sessions} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("validation created %q: %v", path, err)
				}
			}
		})
	}
}
