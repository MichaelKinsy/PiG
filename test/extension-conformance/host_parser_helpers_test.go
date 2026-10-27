package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// helperInput is one argument of the parseFrontmatter, stripFrontmatter and Date.parse helpers.
type helperInput struct {
	name  string
	fn    string
	input string
	want  map[string]any
}

// TestConformance_HostParserHelpersAgreeAcrossSDKs: Pi exports parseFrontmatter and stripFrontmatter (utils/frontmatter.ts, index.ts) and a handler can
// call Date.parse. The Go, Rust and Python SDKs ask the host's parsers (the YAML 1.2 parser that reads skills and prompt templates; the port of V8's date
// parser), from a handler over its own connection and from the factory, before the connection to the host is ready: the first helper call dials the host.
// The Node SDK runs Pi's own code, so its answers are the reference, including the key order of every parsed object.
func TestConformance_HostParserHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	inputs := hostParserHelperInputs()
	run := func(t *testing.T, h *harness, command, prefix, args string) map[string]any {
		t.Helper()
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), command, args) {
			t.Fatalf("fixture has no %s command", command)
		}
		var reported string
		pollUntilConformance(t, command+": no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, prefix); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got
	}
	probe := func(t *testing.T, h *harness, in helperInput) map[string]any {
		args, err := json.Marshal(map[string]string{"fn": in.fn, "input": in.input})
		if err != nil {
			t.Fatal(err)
		}
		return run(t, h, "helper_probe", "helper:", string(args))
	}
	factory := func(t *testing.T, h *harness) map[string]any {
		return run(t, h, "helper_factory_probe", "helper-factory:", "")
	}

	var reference []map[string]any
	var referenceFactory map[string]any
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		for _, in := range inputs {
			reference = append(reference, probe(t, h, in))
		}
		referenceFactory = factory(t, h)
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) != len(inputs) {
		t.Fatalf("the Node reference ran %d of %d inputs", len(reference), len(inputs))
	}
	// Values Pi's code implies, so a mistake shared by every SDK cannot pass.
	for i, in := range inputs {
		if in.want != nil && !reflect.DeepEqual(reference[i], in.want) {
			t.Fatalf("reference for %s = %v, want %v", in.name, reference[i], in.want)
		}
	}
	wantFactory := map[string]any{
		"frontmatter": map[string]any{
			"frontmatter": map[string]any{"z": 1.0, "10": "a", "2": "b", "nested": map[string]any{"y": 1.0, "3": "x", "q r": []any{1.0, 2.0}}},
			"body":        "body text",
			"keys":        []any{"2,10,z,nested", "3,y,q r"},
		},
		"error": map[string]any{"error": true},
		"date":  map[string]any{"ms": 1577934245678.0},
		"strip": map[string]any{"body": "body text"},
	}
	if !reflect.DeepEqual(referenceFactory, wantFactory) {
		t.Fatalf("the Node reference's factory-time results = %v, want %v", referenceFactory, wantFactory)
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if got := factory(t, h); !reflect.DeepEqual(got, referenceFactory) {
				t.Errorf("called while the factory runs: %v, Node (Pi) reports %v", got, referenceFactory)
			}
			for i, in := range inputs {
				if got := probe(t, h, in); !reflect.DeepEqual(got, reference[i]) {
					t.Errorf("%s: %.300v, Node (Pi) reports %.300v", in.name, got, reference[i])
				}
			}
		})
	}
}

func hostParserHelperInputs() []helperInput {
	var inputs []helperInput
	header := func(yaml string) string { return "---\n" + yaml + "\n---\nbody\n" }
	fm := func(name, content string, want map[string]any) {
		inputs = append(inputs,
			helperInput{name: "frontmatter " + name, fn: "frontmatter", input: content, want: want},
			helperInput{name: "strip " + name, fn: "strip", input: content})
	}
	ok := func(frontmatter any, body string, keys ...string) map[string]any {
		out := map[string]any{"frontmatter": frontmatter, "body": body}
		list := []any{}
		for _, k := range keys {
			list = append(list, k)
		}
		out["keys"] = list
		return out
	}
	failure := map[string]any{"error": true}

	fm("pinned key order", "---\nz: 1\n10: a\n2: b\nnested:\n  y: 1\n  3: x\n---\nbody text\n",
		ok(map[string]any{"z": 1.0, "10": "a", "2": "b", "nested": map[string]any{"y": 1.0, "3": "x"}}, "body text", "2,10,z,nested", "3,y"))
	fm("no header", "just a body\n---\nnot a header", ok(map[string]any{}, "just a body\n---\nnot a header", ""))
	fm("empty header", "---\n---\nbody", nil)
	fm("unterminated header", "---\na: 1\nbody", ok(map[string]any{}, "---\na: 1\nbody", ""))
	fm("header at end without a newline", "---\na: 1\n---", nil)
	fm("blank lines after the header", "---\na: 1\n---\n\n\nbody\n", nil)
	fm("CRLF", "---\r\na: 1\r\nb: two\r\n---\r\nbody\r\nmore\r\n", ok(map[string]any{"a": 1.0, "b": "two"}, "body\nmore", "a,b"))
	fm("byte order mark", "\ufeff---\na: 1\n---\nbody", nil)
	fm("leading space", " ---\na: 1\n---\nbody", nil)
	fm("closing marker with trailing text", "---\na: 1\n--- x\nbody\n---\nafter", nil)
	fm("a header inside the body", "---\na: 1\n---\nbody\n---\nb: 2\n---\n", nil)
	fm("scalar document", header("hello"), nil)
	fm("array document", header("- a\n- b\n- {c: 1}"), nil)
	fm("number document", header("42"), nil)
	fm("null document", header("null"), nil)
	fm("comment only", header("# nothing"), nil)
	fm("numbers", header("a: 1.0\nb: 1e3\nc: 0x1F\nd: 0o17\ne: .inf\nf: -.inf\ng: .nan\nh: 1_000\ni: 12345678901234567890\nj: 0.1\nk: -0\nl: 1e21\nm: 1e-7\nn: +5\no: 007\np: 1.5e+3\nq: 0b11"), nil)
	fm("booleans and null", header("a: yes\nb: on\nc: true\nd: ~\ne: null\nf: NULL\ng: False\nh: n\ni: TRUE\nj:"), nil)
	fm("quoted scalars", header("a: \"line\\nbreak\\t\\u00e9 \\U0001F600\"\nb: 'it''s'\nc: \"<>&\\u2028\\u2029\"\nd: \"\\x41\\0\""), nil)
	// The yaml library builds a double-quoted scalar from UTF-16 units, so a high-surrogate escape and the low-surrogate escape after it are one character.
	fm("surrogate pair escapes", header("a: \"\\uD83D\\uDE00 \\U0000D800\\uDC00\"\nb: \"\\uDBFF\\\n  \\uDFFF\""), nil)
	fm("plain scalars", header("a: hello world\nb: 2001-12-14\nc: 12:30:45\nd: http://x.y/z#a\ne: a: b\nf: - x"), nil)
	fm("block scalars", header("t: |\n  a\n  b\n\nu: >\n  c\n  d\n\n  e\nv: |-\n  x\nw: >+\n  y\n\n"), nil)
	fm("anchors and aliases", header("a: &x [1, 2]\nb: *x\nc: &y {k: v}\nd: *y"), nil)
	fm("merge key", header("base: &b {x: 1}\nd:\n  <<: *b\n  y: 2"), nil)
	fm("flow collections", header("{a: 1, b: [x, y, {z: 3}], c: {d: e}, 7: s}"), nil)
	fm("empty collections", header("a:\nb: {}\nc: []\nd: ''\ne: \"\""), nil)
	fm("integer-like keys order", header("b: 1\n4294967295: x\n4294967294: y\n01: z\n-1: n\n1.5: f\n100: h\n9: i\n0: o\na: 2"), nil)
	fm("non-ASCII", header("naïve: café\n日本語: 値\nemoji: 😀\n\"😀\": e"), nil)
	fm("typical skill header", "---\nname: pdf-tools\ndescription: Extract text and tables from PDF files.\nlicense: MIT\ncompatibility: node >= 20\nallowed-tools: Bash(pdftotext:*) Read\nmetadata:\n  author: someone\n  version: \"1.0\"\ndisable-model-invocation: true\n---\n# Skill\n\nUse it.\n", nil)
	fm("duplicate key", header("a: 1\na: 2"), failure)
	fm("unclosed flow", header("a: ["), failure)
	fm("tab indentation", header("a:\n\tb: 1"), failure)
	fm("unknown alias", header("a: *nope"), failure)
	fm("bad indentation", header("a: 1\n  b: 2"), failure)
	fm("unterminated quote", header("a: \"x"), failure)
	fm("an empty string", "", nil)
	var many strings.Builder
	for i := range 300 {
		fmt.Fprintf(&many, "k%d: %d\n%d: v\n", i, i, 299-i)
	}
	fm("many keys", header(strings.TrimSuffix(many.String(), "\n")), nil)
	deep := "a: "
	for range 40 {
		deep += "{b: "
	}
	deep += "1" + strings.Repeat("}", 40)
	fm("deep nesting", header(deep), nil)
	fm("long body", "---\na: 1\n---\n"+strings.Repeat("line é 😀\n", 5000), nil)

	date := func(name, text string, want map[string]any) {
		inputs = append(inputs, helperInput{name: "date " + name, fn: "date", input: text, want: want})
	}
	ms := func(v float64) map[string]any { return map[string]any{"ms": v} }
	nan := map[string]any{"ms": nil}
	date("ISO with milliseconds", "2020-01-02T03:04:05.678Z", ms(1577934245678))
	date("ISO date only is UTC", "2020-01-02", ms(1577923200000))
	date("ISO with an offset", "2020-01-02T03:04:05+01:00", ms(1577930645000))
	date("ISO minutes", "2020-01-02T03:04Z", ms(1577934240000))
	date("ISO year", "2020", ms(1577836800000))
	date("ISO year and month", "2020-06", ms(1590969600000))
	date("ISO extended year", "+275760-09-13T00:00:00.000Z", ms(8.64e15))
	date("ISO past the end of time", "+275760-09-13T00:00:00.001Z", nan)
	date("ISO negative year", "-000001-01-01T00:00:00Z", ms(-62198755200000))
	date("ISO negative zero year", "-000000-01-01T00:00:00Z", nan)
	date("ISO fraction beyond milliseconds", "2020-01-02T03:04:05.1239Z", nil)
	date("ISO with a comma fraction", "2020-01-02T03:04:05,5Z", nil)
	date("ISO 24:00", "2020-01-02T24:00:00Z", ms(1578009600000))
	date("ISO hour 25", "2020-01-02T25:00:00Z", nan)
	date("ISO month 13", "2020-13-01", nan)
	date("ISO February 30", "2020-02-30", nil)
	date("ISO February 30 with a time", "2020-02-30T00:00:00Z", nil)
	date("ISO leap day", "2020-02-29T00:00:00Z", ms(1582934400000))
	date("ISO not a leap year", "2019-02-29T00:00:00Z", nil)
	date("ISO without an offset is local time", "2020-01-02T03:04:05", nil)
	date("ISO with a space", "2020-01-02 03:04:05", nil)
	date("ISO with a space and Z", "2020-01-02 03:04:05Z", nil)
	date("lowercase t and z", "2020-01-02t03:04:05z", nil)
	date("day and month names", "Oct 5 2020", nil)
	date("month name first", "January 1, 2000 12:00 PM", nil)
	date("RFC 2822", "Mon, 06 Jan 2020 10:00:00 GMT", ms(1578304800000))
	date("RFC 2822 with an offset", "Mon, 06 Jan 2020 10:00:00 +0530", nil)
	date("toString form", "Thu Jan 02 2020 03:04:05 GMT+0100 (CET)", ms(1577930645000))
	date("toUTCString form", "Thu, 02 Jan 2020 03:04:05 GMT", ms(1577934245000))
	date("slashes", "2020/01/02", nil)
	date("US order", "1/2/2020", nil)
	date("two-digit year", "1/2/20", nil)
	date("time only", "12:00", nan)
	date("empty", "", nan)
	date("words", "not a date", nan)
	date("whitespace around", "  2020-01-02T03:04:05Z  ", nil)
	date("a number", "1577934245678", nil)
	date("trailing garbage", "2020-01-02T03:04:05Zjunk", nil)
	date("epoch", "1970-01-01T00:00:00Z", ms(0))
	date("before the epoch", "1969-12-31T23:59:59.999Z", ms(-1))
	date("non-ASCII", "２０２０-01-02", nil)
	isoDateInputs(date, ms, nan)
	return inputs
}

// isoDateInputs is the corpus of the form Pi writes every entry timestamp in, YYYY-MM-DDTHH:mm:ss.sssZ (Date.prototype.toISOString), which the Go, Rust and Python
// SDKs parse without the host, and of the texts next to it that they must leave to the host's parser: a missing Z, an offset, a year of six digits, a lowercase t or z,
// a field out of range (V8 reads February 30 as March 2 and hour 24 as the next midnight, and refuses month 13 and second 60) and digits that are not ASCII. Node's own
// Date.parse is the reference for every text; a few answers are pinned to what V8 gives so a mistake every SDK shares cannot pass.
func isoDateInputs(date func(name, text string, want map[string]any), ms func(float64) map[string]any, nan map[string]any) {
	for text, want := range map[string]float64{
		"1970-01-01T00:00:00.000Z": 0, "0000-01-01T00:00:00.000Z": -62167219200000, "0000-03-01T00:00:00.000Z": -62162035200000, "0001-01-01T00:00:00.000Z": -62135596800000,
		"9999-12-31T23:59:59.999Z": 253402300799999, "2000-02-29T12:34:56.789Z": 951827696789, "1900-02-28T23:59:59.999Z": -2203891200001, "1900-03-01T00:00:00.000Z": -2203891200000,
		"2100-03-01T00:00:00.000Z": 4107542400000, "2024-02-29T00:00:00.000Z": 1709164800000, "2026-10-24T18:30:15.042Z": 1792866615042, "1969-12-31T23:59:59.999Z": -1,
		"1600-02-29T00:00:00.000Z": -11670998400000, "2026-12-31T23:59:59.999Z": 1798761599999, "2026-01-01T00:00:00.001Z": 1767225600001,
	} {
		date("pinned entry timestamp "+text, text, ms(want))
	}
	for text, want := range map[string]map[string]any{
		"2026-02-30T00:00:00.000Z": ms(1772409600000), "2026-04-31T00:00:00.000Z": ms(1777593600000), "2026-01-02T24:00:00.000Z": ms(1767398400000),
		"2026-01-02T03:04:05.678+00:00": ms(1767323045678), "2026-01-02t03:04:05.678z": ms(1767323045678), "+012026-01-02T03:04:05.678Z": ms(317336843045678),
		"-000001-01-01T00:00:00.000Z": ms(-62198755200000), "2026-13-01T00:00:00.000Z": nan, "2026-01-02T03:04:60.000Z": nan, "2026-01-02T03:60:05.000Z": nan,
		"2026-00-10T00:00:00.000Z": nan, "2026-01-02T03:04:05.678Z\n": nan, "２０２６-01-02T03:04:05.678Z": nan, "٢٠٢٦-01-02T03:04:05.678Z": nan,
	} {
		date("pinned near miss "+strings.ReplaceAll(text, "\n", "\\n"), text, want)
	}

	rng := rand.New(rand.NewPCG(7, 9))
	pick := func(values ...int) int { return values[rng.IntN(len(values))] }
	daysIn := func(year, month int) int {
		switch month {
		case 2:
			if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
				return 29
			}
			return 28
		case 4, 6, 9, 11:
			return 30
		}
		return 31
	}
	for i := range 300 {
		year := pick(0, 1, 99, 100, 399, 400, 1582, 1600, 1899, 1900, 1969, 1970, 1999, 2000, 2023, 2024, 2025, 2026, 2100, 2400, 9998, 9999, rng.IntN(10000), rng.IntN(10000))
		month := pick(1, 2, 3, 12, 1+rng.IntN(12), 1+rng.IntN(12))
		day := pick(1, daysIn(year, month), 28, 29, 30, 31, 1+rng.IntN(daysIn(year, month)))
		day = min(day, daysIn(year, month))
		text := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%03d", year, month, day, pick(0, 1, 12, 23, rng.IntN(24)), pick(0, 1, 59, rng.IntN(60)), pick(0, 59, rng.IntN(60)), pick(0, 1, 9, 10, 99, 100, 999, rng.IntN(1000)))
		date(fmt.Sprintf("entry timestamp %d", i), text+"Z", nil)
		switch i % 16 {
		case 0:
			date(fmt.Sprintf("near miss without Z %d", i), text, nil)
		case 1:
			date(fmt.Sprintf("near miss with an offset %d", i), text+pick2(rng, "+00:00", "-05:30", "+01:00", "+0100", "-12:00", "+14:00"), nil)
		case 2:
			date(fmt.Sprintf("near miss lowercase t %d", i), strings.Replace(text, "T", "t", 1)+"Z", nil)
		case 3:
			date(fmt.Sprintf("near miss lowercase z %d", i), text+"z", nil)
		case 4:
			date(fmt.Sprintf("near miss six-digit year %d", i), fmt.Sprintf("%+07d", year)[:1]+fmt.Sprintf("%06d", year)+text[4:]+"Z", nil)
		case 5:
			date(fmt.Sprintf("near miss day 32 %d", i), fmt.Sprintf("%04d-%02d-32", year, month)+text[10:]+"Z", nil)
		case 6:
			date(fmt.Sprintf("near miss day 00 %d", i), fmt.Sprintf("%04d-%02d-00", year, month)+text[10:]+"Z", nil)
		case 7:
			date(fmt.Sprintf("near miss month 00 or 13 %d", i), fmt.Sprintf("%04d-%02d-%02d", year, pick(0, 13), day)+text[10:]+"Z", nil)
		case 8:
			date(fmt.Sprintf("near miss hour 24 or 25 %d", i), text[:11]+fmt.Sprintf("%02d", pick(24, 25))+text[13:]+"Z", nil)
		case 9:
			date(fmt.Sprintf("near miss 24:00:00.000 %d", i), text[:11]+"24:00:00.000Z", nil)
		case 10:
			date(fmt.Sprintf("near miss minute or second 60 %d", i), text[:14]+fmt.Sprintf("%02d", 60+rng.IntN(5))+text[16:]+"Z", nil)
		case 11:
			date(fmt.Sprintf("near miss second 60 %d", i), text[:17]+"60"+text[19:]+"Z", nil)
		case 12:
			date(fmt.Sprintf("near miss day past the month %d", i), fmt.Sprintf("%04d-%02d-%02d", year, month, daysIn(year, month)+1)+text[10:]+"Z", nil)
		case 13:
			date(fmt.Sprintf("near miss space or newline %d", i), pick2(rng, " "+text+"Z", text+"Z\n", strings.Replace(text, "T", " ", 1)+"Z", text+"Z "), nil)
		case 14:
			date(fmt.Sprintf("near miss other fraction or no fraction %d", i), pick2(rng, text[:19]+"Z", text[:21]+"Z", text[:22]+"Z", text+"9Z", text[:19]+",5Z"), nil)
		case 15:
			// A character next to the digits in a digit's place: its distance from "0" is a small number, so a parser that does not test the character reads a date.
			at := pick(0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18, 20, 21, 22)
			date(fmt.Sprintf("near miss a non-digit in a digit's place %d", i), text[:at]+pick2(rng, "/", ":", ";", "<", "=", ">", "?", "@", "A", "x")+text[at+1:]+"Z", nil)
		}
	}
	// Digits that are not ASCII, in a text that is 24 characters long (a Python string counts characters, not bytes).
	date("full-width digits in a 24-character text", "２０２６-０１-０２T０３:０４:０５.６７８Z", nan)
	date("Arabic-Indic digits in a 24-character text", "٢٠٢٦-٠١-٠٢T٠٣:٠٤:٠٥.٦٧٨Z", nan)
	date("Devanagari digits in a 24-character text", "२०२६-०१-०२T०३:०४:०५.६७८Z", nan)
	date("a 24-character text of two-byte characters", strings.Repeat("é", 24), nan)
}

func pick2(rng *rand.Rand, values ...string) string { return values[rng.IntN(len(values))] }
