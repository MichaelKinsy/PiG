package collate

import (
	"fmt"
	"testing"

	xcollate "golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The name sets compare 8 to 40 byte strings in pairs, as the ls tool and the file pickers do.
var benchNames = map[string][]string{
	"ascii": {"README.md", "main.go", "Makefile", "config_loader_test.go", "zzz-last-file.txt", "a", "internal_helpers.go", "Docs", "docs", "LICENSE-APACHE-2.0.txt"},
	"latin": {"résumé.txt", "café-menu.md", "naïve_bayes.py", "Zürich-Übersicht.pdf", "señor_ñandú.txt", "façade.go", "Ångström.dat", "crème brûlée.md", "über.go", "éclair.txt"},
	"cjk":   {"你好世界.txt", "日本語のファイル.md", "한국어문서.pdf", "测试文件目录", "ドキュメント.txt", "中文名称1.go", "中文名称2.go", "東京タワー.png", "漢字かな交じり文.txt", "北京烤鸭菜单.md"},
	"mixed": {"report-2024-财务.xlsx", "Café 東京 notes.md", "src/日本語/main.go", "photo_été_01.jpg", "Müller & Söhne.pdf", "notes_ひらがな.txt", "data-北京-2.csv", "résumé_中文.docx", "file (copy) 3.txt", "ÄÖÜ-straße.txt"},
}

func init() {
	// What the ls tool sorts: lower-cased file names with a five-digit counter.
	stems := []string{"file_%05d_name.txt", "résumé_%05d.md", "café-%05d.go", "日本語_%05d.txt", "src-%05d", "readme_%05d", "zürich %05d.pdf", "ångström_%05d.dat"}
	names := make([]string, 1000)
	for i := range names {
		names[i] = fmt.Sprintf(stems[i%len(stems)], (i*7919)%10000)
	}
	benchNames["ls"] = names
}

var benchSink int

func BenchmarkCompare(b *testing.B) {
	for name, set := range benchNames {
		b.Run(name+"/fork", func(b *testing.B) {
			c := New()
			c.CompareString("a", "b")
			b.ReportAllocs()
			for i := range b.N {
				benchSink += c.CompareString(set[i%len(set)], set[(i+3)%len(set)])
			}
		})
		b.Run(name+"/xtext", func(b *testing.B) {
			c := xcollate.New(language.Und)
			c.CompareString("a", "b")
			b.ReportAllocs()
			for i := range b.N {
				benchSink += c.CompareString(set[i%len(set)], set[(i+3)%len(set)])
			}
		})
	}
}

// BenchmarkAppendKey is the path the ls tool sorts by: one key per name, then byte comparison.
func BenchmarkAppendKey(b *testing.B) {
	for name, set := range benchNames {
		b.Run(name+"/fork", func(b *testing.B) {
			c := New()
			buf := c.AppendKey(nil, "a")
			b.ReportAllocs()
			for i := range b.N {
				buf = c.AppendKey(buf[:0], set[i%len(set)])
			}
			benchSink += len(buf)
		})
		b.Run(name+"/xtext", func(b *testing.B) {
			c := xcollate.New(language.Und)
			var buf xcollate.Buffer
			c.KeyFromString(&buf, "a")
			b.ReportAllocs()
			var key []byte
			for i := range b.N {
				buf.Reset()
				key = c.KeyFromString(&buf, set[i%len(set)])
			}
			benchSink += len(key)
		})
	}
}

// BenchmarkKeysOfTenThousandNames builds the keys of 10,000 distinct names into one growing buffer, the way the ls tool does before it sorts.
func BenchmarkKeysOfTenThousandNames(b *testing.B) {
	names := make([]string, 10000)
	for i := range names {
		names[i] = benchNames["ls"][i%1000] + string(rune('a'+i%26))
	}
	b.Run("fork", func(b *testing.B) {
		c := New()
		c.CompareString("a", "b")
		b.ReportAllocs()
		for range b.N {
			var keys []byte
			for _, n := range names {
				keys = c.AppendKey(keys, n)
			}
			benchSink += len(keys)
		}
	})
	b.Run("xtext", func(b *testing.B) {
		c := xcollate.New(language.Und)
		b.ReportAllocs()
		for range b.N {
			var buf xcollate.Buffer
			n := 0
			for _, s := range names {
				n += len(c.KeyFromString(&buf, s))
			}
			benchSink += n
		}
	})
}
