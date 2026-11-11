package json

import (
	"bytes"
	"strings"
	"testing"
)

// appendCompactByByte is appendCompact as it was before string bodies were skipped: every byte goes through the scanner.
func appendCompactByByte(dst, src []byte, escape bool) ([]byte, error) {
	origLen := len(dst)
	scan := newScanner()
	defer freeScanner(scan)
	start := 0
	for i, c := range src {
		if escape && (c == '<' || c == '>' || c == '&') {
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			start = i + 1
		}
		if escape && c == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && src[i+2]&^1 == 0xA8 {
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[src[i+2]&0xF])
			start = i + 3
		}
		v := scan.step(scan, c)
		if v >= scanSkipSpace {
			if v == scanError {
				break
			}
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			start = i + 1
		}
	}
	if scan.eof() == scanError {
		return dst[:origLen], scan.err
	}
	if start < len(src) {
		dst = append(dst, src[start:]...)
	}
	return dst, nil
}

func checkCompactSkip(t testing.TB, src []byte) {
	t.Helper()
	for _, escape := range []bool{false, true} {
		want, wantErr := appendCompactByByte([]byte("pre"), src, escape)
		got, gotErr := appendCompact([]byte("pre"), src, escape)
		if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) {
			t.Fatalf("appendCompact(%q, %v) error %v, byte by byte %v", src, escape, gotErr, wantErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("appendCompact(%q, %v) = %q, byte by byte %q", src, escape, got, want)
		}
	}
}

func TestAppendCompactSkipsStringBodiesLikeTheScanner(t *testing.T) {
	long := strings.Repeat("line of source code with \\\"quotes\\\" and \\n escapes, tabs\\t, slashes \\/ and <tags> & more\\n", 40)
	corpus := []string{
		`{"a": 1, "b" : [ true , false, null ] }`,
		`{"text":"` + long + `","n": 1.5e3}`,
		`[ "plain string longer than eight bytes", "<>&", "\u2028 \u00e9 é 日本語", "\u003c" ]`,
		"{\"a\":\"\xe2\x80\xa8 and \xe2\x80\xa9 and \xe2\x80\xaa\"}",
		`{"esc":"\\\\\"\\n\\r\\b\\f\\/\\u00e9\\u12"}`,
		`{"bad":"\q"}`, `{"bad":"\u12g4"}`, "{\"ctl\":\"a\x01b\"}", `{"open":"abc`, `{"open":"abc\`, `{"open":"abc\\`, `"`, `"\`, ``, `   `,
		`{"k\n":"v\n","<":"&"}`, `{"a":"x"}  `, ` [ "a" , "b" ] x`,
	}
	for _, src := range corpus {
		checkCompactSkip(t, []byte(src))
	}
}

func FuzzAppendCompactSkipsStringBodies(f *testing.F) {
	for _, seed := range []string{`{"a": "b\n<c>"}`, `["\u2028é", 1, {"x" : null}]`, "{\"a\":\"\xe2\x80\xa8\"}", `"\`, `{"a":"\q"}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, src []byte) { checkCompactSkip(t, src) })
}

func TestAppendCompactSkipsUnicodeEscapes(t *testing.T) {
	for _, src := range []string{
		`{"t":"a \u003cdiv\u003e \u0026\u0026 b \u2028 \u00e9 \ud83d\ude00 end"}`,
		`{"t":"\u12"}`, `{"t":"\u12g4 x"}`, `{"t":"\u123`, `{"t":"\u`, `"\u0041\u0042\u0043\u0044\u0045"`,
	} {
		checkCompactSkip(t, []byte(src))
	}
}

// BenchmarkAppendCompactSource is the compaction of a Marshaler's output that holds source code with the escapes an encoder writes for it.
func BenchmarkAppendCompactSource(b *testing.B) {
	text := strings.Repeat("func (s *Session) run(ctx context.Context) error {\\n\\tif a \\u003c b \\u0026\\u0026 c \\u003e d {\\n\\t\\treturn fmt.Errorf(\\\"quoted %q\\\", s.name)\\n\\t}\\n}\\n", 900)
	src := []byte(`{"role":"toolResult","content":[{"type":"text","text":"` + text + `"}],"isError":false}`)
	for name, compact := range map[string]func(dst, src []byte, escape bool) ([]byte, error){"skipping": appendCompact, "byte by byte": appendCompactByByte} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))
			dst := make([]byte, 0, len(src))
			for b.Loop() {
				if _, err := compact(dst[:0], src, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
