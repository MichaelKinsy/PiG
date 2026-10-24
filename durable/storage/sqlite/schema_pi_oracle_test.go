package sqlite_test

// pi: packages/durable/src/storage/sqlite/schema.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's createSqliteSchema and DEFAULT_SQLITE_SCHEMA (schema.ts, imported as-is under Node, with Chord's own
// validateSqliteSchemaPrefix) answer for each prefix below; CreateSqliteSchema must give the same identifiers, field for
// field, and the same error text for a prefix Pi rejects. The prefixes cover none, empty, valid shapes, and every kind of
// invalid one (first character, characters, length, reserved words in either case).

type schemaCase struct {
	Prefix  *string `json:"prefix"`
	Default bool    `json:"default,omitempty"`
}

type schemaAnswer struct {
	Tables  map[string]string `json:"tables"`
	Indexes map[string]string `json:"indexes"`
	Error   *string           `json:"error"`
}

// identifiers is the exported string fields of a struct, keyed by the field name with its first letter lower-cased.
func identifiers(t *testing.T, value any) map[string]string {
	t.Helper()
	v := reflect.ValueOf(value)
	out := map[string]string{}
	for i := range v.NumField() {
		name := []rune(v.Type().Field(i).Name)
		name[0] = unicode.ToLower(name[0])
		out[string(name)] = v.Field(i).String()
	}
	return out
}

func TestSqliteSchemaMatchesPiForEveryPrefix(t *testing.T) {
	prefixes := []string{
		"", "a", "A", "app_", "App", "a1", "a_1_", "_", "_a", "__", "durable_", "tenant42_", "x_y_z_", "ABC_DEF",
		"1", "1bad", "9_", "a-b", "a b", " a", "a ", "a.b", "a$", "a;", "a'", "a\"", "a\n", "é", "aé", "日本", "a\u0000", "-", "a-",
		"sqlite", "sqlite_", "SQLite_", "SQLITE_", "Sqlite_x", "sqlite_master", "sqlite_sequence", "sqlit_", "sqlite1", "xsqlite_",
		strings.Repeat("a", 31), strings.Repeat("a", 32), strings.Repeat("a", 33), strings.Repeat("a", 63), strings.Repeat("a", 64),
		strings.Repeat("a", 65), strings.Repeat("a", 128), strings.Repeat("a_", 40),
	}
	cases := []schemaCase{{Prefix: nil}, {Default: true}}
	for i := range prefixes {
		cases = append(cases, schemaCase{Prefix: &prefixes[i]})
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/schema_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []schemaAnswer
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(cases) {
		t.Fatalf("oracle output: %v (%d answers for %d cases)", err, len(pi), len(cases))
	}
	rejected, accepted := 0, 0
	for i, c := range cases {
		name := "no prefix"
		switch {
		case c.Default:
			name = "the default schema"
		case c.Prefix != nil:
			name = "prefix " + strings.ToValidUTF8(*c.Prefix, "?")
		}
		var got sqlite.SqliteSchema
		var gotErr error
		if c.Default {
			got = sqlite.DefaultSqliteSchema
		} else {
			got, gotErr = sqlite.CreateSqliteSchema(c.Prefix)
		}
		want := pi[i]
		switch {
		case want.Error != nil:
			rejected++
			if gotErr == nil {
				t.Errorf("%s: Pi rejects with %q, PiG accepts %+v", name, *want.Error, got)
			} else if gotErr.Error() != *want.Error {
				t.Errorf("%s: PiG error %q, Pi %q", name, gotErr, *want.Error)
			}
		case gotErr != nil:
			t.Errorf("%s: PiG rejects with %q, Pi accepts", name, gotErr)
		default:
			accepted++
			if tables := identifiers(t, got.Tables); !reflect.DeepEqual(tables, want.Tables) {
				t.Errorf("%s: tables %v, Pi %v", name, tables, want.Tables)
			}
			if indexes := identifiers(t, got.Indexes); !reflect.DeepEqual(indexes, want.Indexes) {
				t.Errorf("%s: indexes %v, Pi %v", name, indexes, want.Indexes)
			}
		}
	}
	// The corpus has to reach both answers, or it would pass on a constant.
	if rejected < 10 || accepted < 10 {
		t.Errorf("Pi rejected %d and accepted %d prefixes", rejected, accepted)
	}
}
