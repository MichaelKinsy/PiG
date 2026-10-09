package yaml12

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// yamlCorpus holds YAML texts of every shape the frontmatter of a prompt or skill takes, and the malformed ones.
func yamlCorpus() []string {
	base := []string{
		"", "\n", "a: 1", "a: 1\n", "a: b: c", "a: 'it''s'", "a: \"x\\ny\"", "a: \"unclosed", "a: 'unclosed", "a: [1, 2", "a: [1, 2]]", "a: {b: 1", "a: {b: 1}}",
		"a:\n  - b\n  - c", "a:\n- b\n- c", "- a\n- b", "- - a\n  - b", "- a\nb: c", "a: |\n  x\n  y", "a: |\n  x\n\n y: z", "a: >\n  x\n  y\n\n  z", "a: |+\n  x\n\n\n", "a: |-\n  x\n", "a: |2\n   x", "a: >-\n  x\n",
		"a: &x 1\nb: *x", "a: *y", "a: !!str 1", "a: !!int abc", "a: !foo bar", "a: !<tag:yaml.org,2002:str> 1", "? a\n: b", "? - a\n  - b\n: c", ": x", "a: -", "a: - b", "a:\n\t- b", "\ta: 1", " a: 1", "a: 1\n b: 2", "a: 1\n  b: 2",
		"a: b\n  c: d", "a: 1\na: 2", "a: 1\nb: 2\na: 3", "{a: 1, b: 2}", "[a, b, c]", "{a, b: , c: d}", "{? a : b}", "[a: b, c]", "[a, [b, c], {d: e}]", "a: [x, y, ]", "a: {c: d, }", "#only comment", "a: 1 # c\nb: 2#d", "a: 1\n... \nb", "a: 1\n...", "- - -", "---\na: 1", "%YAML 1.2\n---\na: 1",
		"description: @bad", "description: `bad", "description: %bad", "description: |\n  ok\n bad", "a:\n  - b\n c: d", "key: value: other", "description: Use when: a thing", "a: \"\\q\"", "a: \"\\u12\"", "a: \"\\xZZ\"", "a: 'x'y", "a: \"x\"y", "'a': \"b\"", "a: 0x1F\nb: 1_000\nc: .inf\nd: 2001-12-14\ne: yes\nf: ~\ng: 1e3\nh: 0o17\ni: 017",
		"1: one\ntrue: t\nnull: n", "a: b #c\nd: e#f", "a: |\n  x\n\tb", "a: 1\r\nb: 2\r\n", "a: \"multi\n  line\"", "a: 'multi\n\n  line'", "a: plain\n  multi\n  line", "é: ü\n😀: 😀 x", "a: \"😀\\u2028\"", "- {a: 1}\n- [b]", "a:\n  b:\n    c: 1\n  d: 2", "a: [\n  b,\n  c\n]", "a: {\nb: 1\n}", "a: [\nb\n]\nc", "a: &a\n  b: 1\nc: *a", "a: !!map {b: 1}", "a: !!seq [1]", "a: !!null", "a: !!bool yes", "a: !!float 1", "a: !!binary aGk=", "<<: {a: 1}\nb: 2", "a: ?x\nb: -y\nc: :z", "a: {b: c: d}", "- a: 1\n  b: 2\n- c: 3", "a:    b", "a:b", "a: b  \n", "key:\n  - a\n  -\n  - c", "a: >\n\n  x", "a: |\n", "a: |\n\nb: 1", "a: 'x'  # c", "a:\n- b\n  c", "a: [b,\n c]", "[a, b]: c", "{a: b}: c", "? |\n  x\n: y", "a: !!str", "a: &x", "&x a: 1", "!!map a: 1", "*a : 1", "a: 1\n- b", "- a\n  b: 1", "a: 1\n\n\n", "\n\na: 1", "a\n", "a", "'a'", "\"a\"", "[", "{", "]", "}", "a: ]", "a: }", "a: ,", "- ,", "- ?", "? ", "-", "- ", ":", "?", "|", ">", "a: |x", "a: >9", "a: |0",
	}
	base = append(base,
		"a:\n\n\nb: 1", "a:\n\n# c\nb: 1", "a:\n  # c\n\nb: 1", "a:\n\n  # c\n\nb: 1\n", "a: # c\n\nb: 1", "a:\n  b:\n\n\n  c: 1\nd: 2", "- a\n\n# c\n- b", "- a\n  # c\n- b", "- a # c\n# d\n- b", "a: 1\n# c\n  # d\nb: 2", "a:\n  - b\n  # c\nd: 1", "a:\n  - b\n\n  # c\n  - e", "key: &anc\n  - x\n  - y", "? a\n? b\n: c", "? a\n\n: b", "? [a, b]\n: c", "[a, b]:\n  c", "{a: b}:\n- c", "a: !!map\n  b: 1", "- !!str a\n- &x b", "a: b\n---\nc: d", "a: b\n---\nc: d\n...\n---\ne", "--- a\n--- b", "--- |\n  x\n", "--- >\n  x\n--- y", "a: 1\n%YAML 1.2\n---\nb: 2", "%TAG ! tag:x,2000:\n---\n!a b", "[a, b]\n---\n{c: d}", "{a: [b, {c: d}]}: e", "a: [b,\n# c\n  d]", "a: {b: 1,\n  c: 2}\nd: 3", "- [a,\n b]\n- c", "- {a: 1}: b", "- ? a", "- : a", "- - - a\n    - b\n  - c\n- d", "a:\n  - b: 1\n    c: 2\n  - d", "a:\n  b: 1\n c: 2", "a:\n    b: 1\n  c: 2", "  a: 1\n  b: 2", "  - a\n  - b", "a: |\n   x\n  y", "a: |\n  x\n  # c\nb: |\n   y", "a: >\n\n\n  x\n\n\n", "a:\n  |\n  x", "a: \"x\\\n  y\"", "a: 'x\n\n y'", "\"a\\\\\": b", "a: b\r\n# c\r\nd: e\r\n", "\ufeffa: 1", "\ufeff", "a: 1\n\t\nb: 2", "a:\t1", "a:\n\t1", "[a]: 1\n[b]: 2", "[\n]", "{\n}", "{a: 1}\n{b: 2}", "a: *b\n*c : d", "- *a\n- &b *c", "&a [b]: c", "!!seq\n- a", "!!map\na: 1", "&a !!str b", "!!str &a b", "- ? a\n  : b\n- ? c", "\ta", "\t# c\na: 1", "a: 1 \t# c", "k: v\n  # c\n", "k:\n  # c\n  v", "k: [a]\n\n\n", "k: {a: b}\n  # c\nj: 1", "a: 😀\nb: \"😀\"\nc: [😀]", "😀: 1\n---\nx: ?", "a: ? b", "? ? a\n  : b\n: c", "a: b\n:c\n", "a\n:\nb", "a: 1\n- b\nc: 2", "- a\n  - b", "- |\n  x\n- >\n  y", "[a, |\n b]", "[a, : b]", "[: b]", "[? a, ? b : c]", "{? a, ? b : c}", "{a: b, : c}", "{ : }", "[,]", "[a,,b]", "{a,,b}", "[a b, c d]", "{a b: c d}")

	base = append(base,
		"a: *x", "&x a: *x", "&a {b: *a, c: [*a]}", "a: &x [1, *x]\nb: *x", "a: &x {b: &y [*x, *y]}", "&a [[*a]]", "a: &x 1\nb: *x\nc: *x", "a: &x [1]\nb: *x\n", "&a [*a]", "a: &x\nb: *x", "*a", "a: &x 1\n*x : 2", "a: &x [1,2,3]\nb: &y [*x,*x,*x,*x,*x,*x,*x,*x,*x,*x]\nc: &z [*y,*y,*y,*y,*y,*y,*y,*y,*y,*y,*y,*y]\nd: [*z,*z,*z,*z,*z,*z,*z,*z,*z]",
		"%YAML 1.2\na: 1", "%YAML 1.1\na: 1", "%YAML 2.0\na: 1", "%YAML x\na: 1", "%YAML\na: 1", "%TAG !e! tag:e.com,2000:\n!e!x a", "%FOO bar\na: 1", "%TAG ! x y z\na", "a: !e!x b", "a: !<tag:x> b", "a: !<!> b", "a: !<tag:x b", "a: ! b", "a: !!str", "a: !x%zz b", "a: !!\u00e9 b",
		"a: 1\n\tb: 2", "a:\n\t- b", "a: \tb", "- \ta", "\n\t\na: 1", "a:\n  b: 1\n\t c: 2",
		"a: \"\\x41\\u0042\\U00000043 \\z\"", "a: \"\\u00zz\"", "a: \"\\xZ\"", "a: \"\\UFFFFFFFF\"", "a: \"\\ud800\"", "a: \"x\\\n  y\"", "a: \"x\\\r\n y\"", "a: \"x  \n\n  y\"", "a: \"\ttab\\ttab\"", "a: \"trailing \n\"", "a: 'trailing  \n  '", "a: 'it''s\n  here'", "a: \"é\\é\"", "a: 'é",
		"a: |\n  x\n   y\n  z\n", "a: >\n  x\n   y\n  z\n\n  w\n", "a: |\nx", "a: |\n x\n  y\n z", "a:\n  b: |\n  x", "a: |1\n  x", "a: >+\n  x\n\n\nb: 1", "a: >-\n\n  x\n", "a: |\n\n\n", "a: |+\n\n\n", "a: |\n  \n  x", "a: |\n\tx", "a: | # c\n  x", "a: |#c\n  x", "a: |x\n  y", "a: |-+\n  x", "a: >2\n   x\n  y", "- |\n x\n- >\n y\n",
		"a: 1\na: 2", "{a: 1, a: 2}", "[a, a]", "? a\n: 1\n? a\n: 2", "1: a\n1.0: b", "null: a\n~: b", "'': a\n'': b", "a:\n  b: 1\n  b: 2",
		"a: 1\n b: 2", "a:\n  - 1\n - 2", "a:\n - 1\n  - 2", "- a\n - b", "a: b\n- c", "- a\nb: c", "a: [1\nb: 2", "a: {x\nb: 2", "[a, b", "{a: b", "a: [b]]", "a: {b}}", "[a, b]]", "a: {x: [}", "a: [{]", "a: [\n  b,\n  c\n  ]", "a: [\nb\n]\n", "a: {\n  b: 1\n}", "x:\n  [\n  a\n  ]",
		"a: b: c", "a: b:c", "a:\n  b: c: d", "- a: b: c", "a: 'x' y", "a: \"x\" y", "a: [b] c", "a: {b: c} d", "[a] b", "{a: b} c", "a: &x b c", "a: !!str b c", "a: &x\n  - b", "&x\n- a", "a:\n  &x\n  - b", "a: !!seq\n- b", "a:\n  !!seq\n  - b", "a: &x !!str b", "a: !!str &x b", "a: &x &y b", "a: !!str !!int b", "&x", "&x a", "!!str", "!!str a", "!!str\na: 1",
		"? a\n? b", "? a\nb", "? a\n  b\n: c", "? a\n: b\n: c", ": a\n: b", "? \n: a", "?\n:\n", "? a\n:", "a:\n?", "? - a\n? - b", "? { a }\n: x", "[ ? a : b, ? c ]", "{ ? a : b, c }", "[a: b: c]", "[a:b]", "[\"a\":b]", "{\"a\":b}", "{a:b}", "{a: b:c}", "[a, b: c, d]", "[[a]: b]", "[{a}: b]",
		"a: 0o17\nb: 0x1F\nc: 0b11\nd: 017\ne: +12\nf: -0\ng: -.5\nh: 1_000\ni: .1e2\nj: 1.e+3\nk: 12e\nl: .inf\nm: -.INF\nn: .NaN\no: 0xG", "a: 99999999999999999999\nb: 0xFFFFFFFFFFFFFFFFFF\nc: 1e400\nd: -1e400\ne: 9007199254740993", "a: Null\nb: NULL\nc: nULL\nd: True\ne: TRUE\nf: tRUE\ng: yes\nh: False\ni: ~\nj:", "a: 1e3\nb: 1E3\nc: 1e+3\nd: 1e-3\ne: .5\nf: 5.\ng: 5.0\nh: 0.0\ni: -0.0\nj: +0.5",
		"a: !!int 1\nb: !!int abc\nc: !!float 1\nd: !!float abc\ne: !!bool true\nf: !!bool yes\ng: !!null ''\nh: !!null x\ni: !!str 1\nj: !!str true\nk: !!int 0x1f\nl: !!float .inf", "a: !!map {b: 1}\nc: !!seq [1]\nd: !!map [1]\ne: !!seq {a: 1}\nf: !!map\n  g: 1\nh: !!str {a: 1}", "a: !!int [1]", "!!str\n",
		"description: Use this when: you need it", "description: \"a: b\"", "description: a #b", "description: a#b", "name: x\ndescription: |\n  Multi\n  line\n\nallowed-tools: Read", "name: \u00e9\u00e8\ndescription: \U0001F600 x\n\u00ff: [\U0001F600, \"\U0001F600\"]", "name: x\n\n\n\ndescription: y\n", "name: x\r\ndescription: y\r\n", "# only\n# comments\n", "\n\n", "   ", " \n a: 1", "name:    x   \ndescription:   y  ",
		"a: 1\n...\nb: 2", "a: 1\n---\nb: 2", "---\n---\n", "a\n---\nb", "--- a\n...\n--- b", "...", "--- ...", "a: 1\n... x", "a: 1\n...\n", "a: 1\n--- # c\n", "%YAML 1.2\n---\na: 1\n---\nb: 2",
		"key: "+strings.Repeat("x", 120)+" [", strings.Repeat("a", 90)+": [", "a: "+strings.Repeat("longword ", 20)+"\n  b: c", "x: 1\n"+strings.Repeat("  ", 50)+"y: [", "a: \""+strings.Repeat("é", 100)+"\\q\"", "- a\n- b\n- c\n- d: e\n   f: g\n- h",
		"a: b\n  # c\n d: e", "a:\n  # c\n  b: 1\n # d\n  c: 2", "a: [b, # c\n  d]", "a: b# c", "a: [b,# c\n]", "a: 'b'# c", "a: |# c\n x", "- # c\n  a", "a: # c\n  b", "a: !!str # c\n  b", "a: &x # c\n  b", "&x # c\n a: 1", "a:\n  - &x # c\n    b",
		"a: \x00", "a: \u2028", "a: \ufeffb", "\ufeffa: 1\n\ufeffb: 2", "a: b\u00a0c", "a:\u00a0b", "\u00a0a: b",
	)

	base = append(base,
		"a: >\n  x\n\n   y\n  z", "a: >\n  x\n   y\n\n   z\n  w", "a: |\n      \n  x", "a: >\n      \n  x\n", "a: |2\n      \n   x", "a: |2\n   \n  x",
		"a: &a [x]\nb: ["+strings.TrimSuffix(strings.Repeat("*a, ", 99), ", ")+"]", "a: &a [x]\nb: ["+strings.TrimSuffix(strings.Repeat("*a, ", 100), ", ")+"]", "a: &a [x]\nb: ["+strings.TrimSuffix(strings.Repeat("*a, ", 101), ", ")+"]",
		strings.Repeat("k", 1022)+": v", strings.Repeat("k", 1023)+": v", strings.Repeat("k", 1024)+": v", strings.Repeat("k", 1025)+": v", "["+strings.Repeat("k", 1023)+": v]", "["+strings.Repeat("k", 1024)+": v]", "["+strings.Repeat("k", 1025)+": v]", "["+strings.Repeat("k", 1026)+": v]",
	)

	base = append(base,
		"a: !!binary aGk=", "a: !!binary |\n  aGVsbG8gd29ybGQ=\n", "a: !!binary 'a G k ='", "a: !!binary a-_b", "a: !!binary =aGk", "a: !!binary aGk", "a: !!binary a", "a: !!binary ''", "a: !!binary 5", "a: !!binary [x]",
		"a: !!timestamp 2001-12-14", "a: !!timestamp 2001-12-14t21:59:43.10-05:00", "a: !!timestamp 2001-12-14 21:59:43.10 -5", "a: !!timestamp 2001-12-14T21:59:43Z", "a: !!timestamp 2001-12-14T21:59:43.123456+05:30", "a: !!timestamp abc", "a: !!timestamp 2001-13-45", "a: !!timestamp 2001-1-1", "a: !!timestamp 2001-12-14T21:59:43+5",
		"a: !!set {x, y}", "a: !!set {x: 1}", "a: !!set\n  ? x\n  ? y\n", "a: !!set [x]", "a: !!set {x, x}", "a: !!set {x: , y: }", "a: !!set {x: ~}", "a: !!set {x: # c\n  }", "!!set {a, b}", "a: !!set {}",
		"a: !!omap [x: 1, y: 2]", "a: !!omap\n  - x: 1\n  - y: 2\n", "a: !!omap [x: 1, x: 2]", "a: !!omap [x]", "a: !!omap [x: 1, y: 2, z]", "a: !!omap {x: 1}", "a: !!omap [{x: 1, y: 2}]", "a: !!omap [[x]]",
		"a: !!pairs [x: 1, x: 2]", "a: !!pairs\n  - x: 1\n  - x: 2\n", "a: !!pairs [x, y]", "a: !!pairs [{x: 1, y: 2}]", "a: !!pairs {x: 1}", "a: !!pairs []",
		"a: !!merge <<", "a: !!merge x", "<<: {x: 1}\nb: 2", "!!merge <<: {x: 1}\nb: 2", "? !!merge <<\n: [{x: 1}, {y: 2}]\nz: 3", "!!merge <<: &m {x: 1}\nb: *m", "!!merge <<: 5", "!!merge <<: [5]", "x: 1\n!!merge <<: {x: 2, y: 3}", "!!merge <<: !!merge <<",
		"a: !!float 1", "a: !!int 5\nb: !!set {c}", "a: !!str\nb: !!omap [c: 1]",
		"[a, b]: c", "{a: 1, b: 2}: x", "[1, 2.5, true, null, ~, , x]: v", "? [a, [b, c], {d: e}]\n: v", "? {a, b}\n: v", "? []\n: v", "? {}\n: v", "? [\"a b\", 'c', \"d\\ne\"]\n: v", "? [a, 'x: y', \"q,r\", '[s]']\n: v",
		"? [0x1F, 0o17, 1e3, .5, 1.50, .inf, -.inf, .nan, +1, 1_0]\n: v", "? [yes, 'true', \"1\", '~', 'null', '']\n: v", "? [&a x, *a, !!str 1, !foo bar, !!int 5, !<tag:x> y]\n: v", "? &k [a]\n: v", "? !!seq [a]\n: v", "? !!map {a: b}\n: v", "? !!set {a}\n: v", "? !!omap [a: 1]\n: v", "? !!pairs [a: 1]\n: v",
		"? ["+strings.Repeat("longword ", 20)+"]\n: v", "? [\""+strings.Repeat("longword ", 20)+"\"]\n: v", "? {"+strings.Repeat("k", 100)+": "+strings.Repeat("w ", 60)+"}\n: v", "? [a,\n  b]\n: v", "? [a, # c\n  b]\n: v", "? [a,\n\n  b]\n: v", "? {a: b, # c\n  c: d}\n: v", "? {a: # c\n  b}\n: v", "? [# c\n  a]\n: v", "? [a] # c\n: v",
		"? [\u00e9, \U0001F600, \"\\u0000\", \"\\t\", \"\\x7f\"]\n: v", "? [a\n  b]\n: v", "? ['a\n  b']\n: v", "? [\"a\n\n  b\"]\n: v", "? [---, '...', '%x', \"- a\", '? b', ': c', '#d', 'e #f', 'g: h', 'i:']\n: v",
		"? [*x]\n: v", "&x [a]: 1\n*x : 2", "? [a]\n: &v [b]\n? *v\n: 3", "[[a]]: 1", "{[a]: b}: 1", "[{a: b}]: 1", "? {? [a] : b}\n: 1", "? {[a]: b}\n: 1",
		"? - a\n  - b\n: 1", "? a: 1\n  b: 2\n: 3", "? |\n  x\n: 1", "? !!str\n: 1", "? - !!set\n    ? a\n: 1",
	)
	base = append(base,
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\nb\"]\n: v",
		"? [\"x\\n\\n\\ny longer text here to pass forty chars total ok\"]\n: v",
		"? [\"trailing \\n lead space and more text to be long enough here\"]\n: v",
		"? [\"a\\x01b\"]\n: v",
		"? [\"a\\u0007b\\u000bc\\u001bd\\u0085e\\u00a0f\\u2028g\\u2029h\\u0000i\"]\n: v",
		"? {? }\n: v",
		"? [? ]\n: v",
		"? {: x}\n: v",
		"? [: ]\n: v",
		"? {: }\n: v",
		"? [ : ]\n: v",
		"? [a, # \n b]\n: v",
		"? [a, #\n b]\n: v",
		"? [a # c\n, b]\n: v",
		"? [\n # c\n a, b]\n: v",
		"? [\n # c1\n # c2\n a, b]\n: v",
		"? {a # c\n : b}\n: v",
		"? {a: b # c\n}\n: v",
		"? {a: # c\n b}\n: v",
		"? {# before\n a: b}\n: v",
		"? [a: # c\n b, c]\n: v",
		"? [*x # c\n : 1]\n&x y: 1",
		"? [&x a, *x # c\n]\n: v",
		"? [?x : y]\n: v",
		"a: !!set {x: !!null }",
		"a: !!set {x: !foo }",
		"a: !!set {x: !!null ~}",
		"a: !!set {x: &n }",
		"a: !!set {x: # c\n }",
		"a: !!set {? x # c\n }",
		"a: !!set {x: ~ # c\n }",
		"? [aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]\n: v",
		"? [aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]\n: v",
		"? [aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]\n: v",
		"? [aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]\n: v",
		"? [aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]\n: v",
		"? {\"--- a\": 1}\n: v",
		"? [\"... x\"]\n: v",
		"? [\"% x\"]\n: v",
		"? [\"x\\n--- y\"]\n: v",
		"? {a: \"--- b\"}\n: v",
		"a: !!timestamp 2001-12-14T21:59:43+0:30",
		"a: !!timestamp 2001-12-14T21:59:43+29",
		"a: !!timestamp 2001-12-14T21:59:43-1:29",
		"a: !!timestamp 2001-12-14T21:59:43+1:30",
		"a: !!timestamp 0050-06-07",
		"a: !!timestamp 0000-01-01",
		"a: !!timestamp 0099-12-31",
		"a: !!timestamp 0100-01-01",
		"!!merge '<<': 1",
		"? [!!merge <<]\n: v",
		"!!merge <<: {!!merge '<<': 1}\nb: 2",
		"? !!merge '<<'\n: 1",
		"? [!!merge '<<']\n: 1",
		"? [&a longword longword longword longword longword longword longword longword longword longword longword longword longword longword]\n: v",
		"? [!!str longword longword longword longword longword longword longword longword longword longword longword longword longword longword]\n: v",
		"? [&a !!str longword longword longword longword longword longword longword longword longword longword longword longword longword longword]\n: v",
		"? {a: longword longword longword longword longword longword longword longword longword longword longword longword longword longword}\n: v",
		"? {kkkkkkkkkkkkkkkkkkkkkkkkkkkkkk: longword longword longword longword longword longword longword longword longword longword longword longword longword longword}\n: v",
		"? {? a : longword longword longword longword longword longword longword longword longword longword longword longword longword longword}\n: v",
		"? {a: &x longword longword longword longword longword longword longword longword longword longword longword longword longword longword}\n: v",
		"? [!!binary QUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJD]\n: v",
		"? [!!binary \"QUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJD\"]\n: v",
		"? [!!binary |\n  QUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJD\n]\n: v",
		"? [!!binary aGk=]\n: v",
		// A Buffer is a property key through String(): its UTF-8 text, with U+FFFD for each maximal invalid subsequence.
		"!!merge <<: {? !!binary /w== : 2}", "!!merge <<: {? !!binary 4oI= : 2}", "!!merge <<: {? !!binary 4oKsgA== : 2}", "!!merge <<: {? !!binary 7aCA : 2}", "!!merge <<: {? !!binary 8J+YgA== : 2}",
		"!!merge <<: {? [!!binary aGk=, 1] : 2}",
		// Keys that are objects or symbols in JavaScript equal no other key: two equal binaries, timestamps or merge keys are not duplicates, and comparing them must not compare Go slices.
		"!!omap\n- ? !!binary aGk=\n  : 1\n- ? !!binary aGk=\n  : 2", "!!set\n? !!binary aGk=\n? !!binary aGk=", "? !!binary aGk=\n: 1\n? !!binary aGk=\n: 2",
		"? !!timestamp 2001-12-14\n: 1\n? !!timestamp 2001-12-14\n: 2", "!!omap\n- !!timestamp 2001-12-14: 1\n- !!timestamp 2001-12-14: 2", "!!pairs\n- !!merge <<: 1\n- !!merge <<: 2", "!!omap\n- !!merge <<: 1\n- !!merge <<: 2",
		"!!omap\n- .nan: 1\n- .NaN: 2", "? .nan\n: 1\n? .nan\n: 2", "!!omap\n- 0: 1\n- -0: 2", "!!omap\n- a: 1\n- a: 2\n- a: 3", "? !!binary /w==\n: v", "? !!binary 4oI=\n: v", "? [!!binary /w==]\n: v",
		"? [!!binary 'aGk=']\n: v",
		"? {a: !!binary QUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJD}\n: v",
		"? [&a\u0019b x]\n: v",
		"? [&a\u0018b x]\n: v",
		"? [&a\u001fb x]\n: v",
		"? [&a b x]\n: v",
		"? !!set {a: ~, b}\n: v",
		"? [!!set {a}, !!omap [x: 1]]\n: v",
		"? [!!timestamp 2001-12-14, !!timestamp 2001-12-14T01:02:03Z]\n: v",
		"? [1.50, 1e3, 0x1F, 0o17, 1_0, -0, .5, +1, 1e-7]\n: v",
		"? [0x1F, 0X1f, 0o17, 017, 1E3, 1.0e+3]\n: v",
		"? [.inf, -.inf, .nan, 1e400]\n: v",
		"? [!!float 1, !!int 0x10, !!float 1.50, !!int 7]\n: v",
		"? [1.10, 2.0, 3.000, 4.5e3, 1e21, 1e-7]\n: v",
		"? [123456789012345678901234567890, 0.000001, 1.5e300]\n: v",
		"? {---x: 1}\n: v",
		"? [---x]\n: v",
		"? {...x: 1}\n: v",
		"? [...x, ---]\n: v",
		"!!merge <<: {!!merge '<<': 1}\nb: 2",
		"!!merge <<: {? {a: 1} : 2}",
		"!!merge <<: {? [a, [b, c]] : 2}",
		"!!merge <<: {? 1 : 2, ? true : 3, ? ~ : 4, ? 1.5: 5}",
		"!!merge <<: {? [a, ~, b] : 2}",
		"!!merge <<: {? !!set {a} : 2}",
		"!!merge <<: {? !!timestamp 2001-12-14 : 2}",
		"!!merge <<: {? !!binary aGk= : 2}",
		"!!merge <<: [{? [a] : 1}, {? [a] : 2}]",
		"? [a,\n # c1\n # c2\n b]\n: v",
		"? {a: 1,\n # c1\n # c2\n b: 2}\n: v",
		"? [a,\n # c1\n b]\n: v",
		"? [a: 1,\n\n # c1\n\n b: 2]\n: v",
		"? [a,\n\n b]\n: v",
		"? [a # c\n : b]\n: v",
		"? {? a # c\n : b}\n: v",
		"? {? a # c\n }\n: v",
		"? [? a # c\n ]\n: v",
		"? [? a # c1\n # c2\n : b]\n: v",
		"? [a: b # c\n, c]\n: v",
		"? [a: # c\n]\n: v",
		"? [a #\n : b]\n: v",
		"? {k: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {k: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {k: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kkk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kkk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kkk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kkkk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kkkk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kkkk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kkkkk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kkkkk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kkkkk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kkkkkk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kkkkkk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kkkkkk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kkkkkkk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kkkkkkk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kkkkkkk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? {kkkkkkkk: wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww}\n: v",
		"? {kkkkkkkk: wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww}\n: v",
		"? {kkkkkkkk: wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww}\n: v",
		"? [&a www www www www www www www www www www www www www www]\n: v",
		"? [www www www www www www www www www www www www www www, xxx xxx xxx]\n: v",
		"? [&a wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww]\n: v",
		"? [wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww wwww, xxxx xxxx xxxx]\n: v",
		"? [&a wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww]\n: v",
		"? [wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww, xxxxx xxxxx xxxxx]\n: v",
		"? [&a wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww]\n: v",
		"? [wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww wwwwww, xxxxxx xxxxxx xxxxxx]\n: v",
		"? [&a wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww]\n: v",
		"? [wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww wwwwwww, xxxxxxx xxxxxxx xxxxxxx]\n: v",
		"? [&a wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww]\n: v",
		"? [wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww wwwwwwww, xxxxxxxx xxxxxxxx xxxxxxxx]\n: v",
		"? [&a wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww]\n: v",
		"? [wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww wwwwwwwww, xxxxxxxxx xxxxxxxxx xxxxxxxxx]\n: v",
		"? [&a wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww]\n: v",
		"? [wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww wwwwwwwwww, xxxxxxxxxx xxxxxxxxxx xxxxxxxxxx]\n: v",
		"? [&a wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww]\n: v",
		"? [wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww wwwwwwwwwww, xxxxxxxxxxx xxxxxxxxxxx xxxxxxxxxxx]\n: v",
		"!!merge <<: {? [!!merge <<] : 2}",
		"!!merge <<: {? [[!!merge <<]] : 2}",
		"!!merge <<: {? [a, !!merge <<] : 2}",
		"? {a: 1, # c\n b: 2}\n: v",
		"? {a: 1 # c\n , b: 2}\n: v",
		"? {a # c\n : 1, b: 2}\n: v",
		"? [a, b # c\n]\n: v",
		"? {a: b, c # x\n}\n: v",
		"? {? a # x\n : b, c: d}\n: v",
		"? [? a # x\n , b]\n: v",
		"? {a:\n # c\n b}\n: v",
		"? {\n # c\n a: b}\n: v",
		"? {a: b, # c1\n # c2\n c: d}\n: v",
		"? {a: b,\n # c1\n # c2\n c: d}\n: v",
		"? [\n # lead1\n # lead2\n a,\n # mid1\n # mid2\n b]\n: v",
		"? {a: # c1\n # c2\n b}\n: v",
		"? [x, # c1\n # c2\n y]\n: v",
		"? [a, # long comment text here\n b, # another one\n c]\n: v",
		"? {k: v, # trailing\n # next\n k2: v2}\n: v",
		"? [{a: 1, # c\n b: 2}, c]\n: v",
		"? [[a, # c\n b], c]\n: v",
		"? [\n a: # c\n b, # d\n c: d]\n: v",
		"? [[a,\n # c1\n # c2\n b]]\n: v",
		"? [{a: 1,\n # c1\n # c2\n b: 2}]\n: v",
		"? {k: [a,\n # c1\n # c2\n b]}\n: v",
		"? [[\n # c1\n # c2\n a]]\n: v",
		"? [[a # c1\n , b]]\n: v",
		"? [[a, b # c1\n]]\n: v",
		"? [{? a # c\n : 1}]\n: v",
		"? [{a # c\n : 1}]\n: v",
		"? [[a # c\n : 1]]\n: v",
		"? [[? a # c\n ]]\n: v",
		"? {? [a # c\n ] : 1}\n: v",
		"? [[a: 1, # c\n b: 2]]\n: v",
		"? {a: {b # c\n : 1}}\n: v",
		"? {a: [? b # c\n , c]}\n: v",
		"? [[ # c\n a]]\n: v",
	)
	return base
}

func hasLongLine(texts []string) bool {
	for _, t := range texts {
		if strings.Contains(t, "\n") {
			return true
		}
	}
	return false
}

// TestLexerMatchesTheLibrary compares the CST token stream of Lexer in eemeli/yaml 2.9.0 with this port's.
func TestLexerMatchesTheLibrary(t *testing.T) {
	corpus := yamlCorpus()
	var want [][]string
	pioracle.Run(t, `
const { Lexer } = await import(new URL("file://" + root + "/../yaml/index.js").href);
emit(input.map((src) => [...new Lexer().lex(src)]));`, corpus, &want)
	for i, src := range corpus {
		got := lex(src)
		if len(got) != len(want[i]) {
			t.Errorf("%q: %d tokens %q, library %d %q", src, len(got), got, len(want[i]), want[i])
			continue
		}
		for j := range got {
			if got[j] != want[i][j] {
				t.Errorf("%q token %d: %q, library %q", src, j, got[j], want[i][j])
				break
			}
		}
	}
	if !hasLongLine(corpus) {
		t.Fatal("corpus lost its multi-line texts")
	}
}

// utf16Offsets maps each byte offset of src to the offset of the same position in the UTF-16 string the library reads.
func utf16Offsets(src string) []int {
	out := make([]int, len(src)+1)
	n := 0
	for i, r := range src {
		for j := i; j < len(src) && (j == i || !utf8.RuneStart(src[j])); j++ {
			out[j] = n
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	out[len(src)] = n
	return out
}

func canonTokens(ts []*Token, off []int) []any {
	out := make([]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, canonToken(t, off))
	}
	return out
}

func canonToken(t *Token, off []int) any {
	if t == nil {
		return nil
	}
	m := map[string]any{"type": t.Type, "offset": off[t.Offset]}
	switch t.Type {
	case "error":
		m["message"], m["source"] = t.Message, t.Source
	case "directive":
		m["source"] = t.Source
	case "document":
		m["start"] = canonTokens(t.Start, off)
		if t.Value != nil {
			m["value"] = canonToken(t.Value, off)
		}
	case "doc-end":
		m["source"] = t.Source
	case "block-scalar":
		m["indent"], m["props"], m["source"] = t.Indent, canonTokens(t.Props, off), t.Source
	case "block-map", "block-seq":
		m["indent"], m["items"] = t.Indent, canonItems(t.Items, off)
	case "flow-collection":
		m["indent"], m["start"], m["items"], m["end"] = t.Indent, canonToken(t.StartTok, off), canonItems(t.Items, off), canonTokens(t.End, off)
	default:
		m["indent"], m["source"] = t.Indent, t.Source
	}
	if t.End != nil && t.Type != "flow-collection" {
		m["end"] = canonTokens(t.End, off)
	}
	return m
}

func canonItems(items []*Item, off []int) []any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		m := map[string]any{"start": canonTokens(it.Start, off)}
		if it.Key != nil {
			m["key"] = canonToken(it.Key, off)
		}
		if it.Sep != nil {
			m["sep"] = canonTokens(it.Sep, off)
		}
		if it.Value != nil {
			m["value"] = canonToken(it.Value, off)
		}
		if it.ExplicitKey {
			m["explicitKey"] = true
		}
		out = append(out, m)
	}
	return out
}

// TestParserMatchesTheLibrary compares the CST of Parser in eemeli/yaml 2.9.0 with this port's, token by token.
func TestParserMatchesTheLibrary(t *testing.T) {
	corpus := yamlCorpus()
	var want []any
	pioracle.Run(t, `
const { Parser } = await import(new URL("file://" + root + "/../yaml/index.js").href);
const canon = (v) => {
  if (Array.isArray(v)) return v.map(canon);
  if (v && typeof v === "object") {
    const o = {};
    for (const k of Object.keys(v).sort()) if (v[k] !== null && v[k] !== undefined) o[k] = canon(v[k]);
    return o;
  }
  return v;
};
emit(input.map((src) => canon([...new Parser().parse(src)])));`, corpus, &want)
	for i, src := range corpus {
		got := canonTokens(parseCST(src), utf16Offsets(src))
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want[i])
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("%q:\n got %s\nwant %s", src, trunc(gotJSON), trunc(wantJSON))
		}
	}
}

func trunc(b []byte) string {
	if len(b) > 600 {
		return string(b[:600]) + "…"
	}
	return string(b)
}
