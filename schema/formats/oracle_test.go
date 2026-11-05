package formats_test

import (
	"os"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

// pi: packages/schema/src/formats/typebox/date.ts
// pi: packages/schema/src/formats/typebox/date_time.ts
// pi: packages/schema/src/formats/typebox/duration.ts
// pi: packages/schema/src/formats/typebox/email.ts
// pi: packages/schema/src/formats/typebox/hostname.ts
// pi: packages/schema/src/formats/typebox/idn_email.ts
// pi: packages/schema/src/formats/typebox/idn_hostname.ts
// pi: packages/schema/src/formats/typebox/ipv4.ts
// pi: packages/schema/src/formats/typebox/ipv6.ts
// pi: packages/schema/src/formats/typebox/iri.ts
// pi: packages/schema/src/formats/typebox/iri_reference.ts
// pi: packages/schema/src/formats/typebox/json_pointer.ts
// pi: packages/schema/src/formats/typebox/json_pointer_uri_fragment.ts
// pi: packages/schema/src/formats/typebox/relative_json_pointer.ts
// pi: packages/schema/src/formats/typebox/time.ts
// pi: packages/schema/src/formats/typebox/uri.ts
// pi: packages/schema/src/formats/typebox/uri_reference.ts
// pi: packages/schema/src/formats/typebox/uri_template.ts
// pi: packages/schema/src/formats/typebox/url.ts
// pi: packages/schema/src/formats/typebox/uuid.ts
// pi: packages/schema/src/formats/typebox/idna/format/bidi.ts
// pi: packages/schema/src/formats/typebox/idna/format/puny.ts
// pi: packages/schema/src/formats/typebox/idna/hostname.ts
// pi: packages/schema/src/formats/typebox/idna/idn-hostname.ts
// pi: packages/schema/src/formats/typebox/idna/label/ascii.ts
// pi: packages/schema/src/formats/typebox/idna/label/puny.ts
// pi: packages/schema/src/formats/typebox/idna/label/unicode.ts
// pi: packages/schema/src/formats/typebox/idna/pattern/pattern.ts

// The rows of testdata/oracle-corpus.json are the results of Pi's own formats (packages/schema/src/formats/index.ts) under Node 24. testdata/gen/gen-corpus.mjs generates them from a Pi checkout (`node gen-corpus.mjs <pi>/packages/schema oracle-corpus.json`); its random rows come from a seeded generator, so a rerun reproduces the file. A value with a lone surrogate is written as the escape JSON.stringify writes, and the test reads it back as the WTF-8 bytes of a Go string. The corpus has no regex rows: the regex format is checked by the suite files of TestConformance. The test replays every row.

type oracleRow struct {
	Format string `json:"format"`
	Value  string `json:"value"`
	Valid  bool   `json:"valid"`
}

func TestOracleCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/oracle-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []oracleRow
	if err := jsstring.UnmarshalKeepingSurrogates(data, &rows); err != nil {
		t.Fatal(err)
	}
	table := formats.Formats()
	perFormat := map[string]int{}
	loneSurrogates := map[string]int{}
	for _, row := range rows {
		check, ok := table[row.Format]
		if !ok {
			t.Errorf("format %q is not in Formats()", row.Format)
			continue
		}
		perFormat[row.Format]++
		if !utf8.ValidString(row.Value) {
			loneSurrogates[row.Format]++
		}
		if got := check(row.Value); got != row.Valid {
			t.Errorf("%s(%q) = %v, Pi says %v", row.Format, row.Value, got, row.Valid)
		}
	}
	for name := range table {
		if name == "regex" {
			continue
		}
		if perFormat[name] < 30 {
			t.Errorf("corpus has %d rows for %s, want at least 30", perFormat[name], name)
		}
		if loneSurrogates[name] < 20 {
			t.Errorf("corpus has %d rows with a lone surrogate for %s, want at least 20", loneSurrogates[name], name)
		}
	}
}
