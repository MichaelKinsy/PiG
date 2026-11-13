package hljs

import (
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

// frameChain streams a text through HighlightResumable as tui's highlightResumable does: each frame is a longer text, the run over it resumes from the last point an earlier frame offered, and the HTML of the frame is the HTML kept before that point joined with the HTML of the resumed run. A frame that offers no point leaves the earlier one in place, and a text that does not start with the text the point was recorded over starts again.
type frameChain struct {
	r    *Registry
	lang string
	// seen is the text of the run that offered point, and head the HTML before point.
	seen  string
	head  string
	point ResumePoint
	// resumedAt is the byte offset the last frame resumed at.
	resumedAt int
	// frames counts the frames, resumed those that resumed from a point, engine the units handed to the engine and units the units of the frames' texts; last is the units of the last frame's text.
	frames, resumed, engine, units, last int
}

// frame highlights text, the next frame, and returns its HTML; ok is false when highlight.js throws for the text.
func (c *frameChain) frame(text string) (html string, ok bool) {
	if !strings.HasPrefix(text, c.seen) {
		c.seen, c.head, c.point = "", "", ResumePoint{}
	}
	source := NewText(text)
	tail, points, err := c.r.HighlightResumable(source, c.lang, true, c.point)
	if errors.Is(err, ErrResumeFailed) {
		c.seen, c.head, c.point = "", "", ResumePoint{}
		tail, points, err = c.r.HighlightResumable(source, c.lang, true, ResumePoint{})
	}
	if err != nil {
		return "", false
	}
	c.resumedAt = c.point.Byte
	c.frames++
	if c.point.Byte > 0 {
		c.resumed++
	}
	c.engine += len(source.units) - c.point.Unit
	c.units += len(source.units)
	c.last = len(source.units)
	html = c.head + tail
	if len(points) > 0 {
		last := points[len(points)-1]
		c.head += tail[:last.HTML]
		c.seen, c.point = text, last
	}
	return html, true
}

// frameStats sums the chains of one cut schedule.
type frameStats struct {
	inputs, bytes, frames, resumed, engine, units atomic.Int64
	// byLanguage, when it is made, sums the chains of each language; mu guards it.
	mu         sync.Mutex
	byLanguage map[string]*languageFrames
}

// languageFrames is the streams of one language: texts counts them, units the units of the whole texts, and engine the units their frames handed to the engine.
type languageFrames struct{ texts, units, engine int }

func (s *frameStats) add(c *frameChain, code string) {
	s.inputs.Add(1)
	s.bytes.Add(int64(len(code)))
	s.frames.Add(int64(c.frames))
	s.resumed.Add(int64(c.resumed))
	s.engine.Add(int64(c.engine))
	s.units.Add(int64(c.units))
	if s.byLanguage == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.byLanguage[c.lang]
	if row == nil {
		row = &languageFrames{}
		s.byLanguage[c.lang] = row
	}
	row.texts++
	row.units += c.last
	row.engine += c.engine
}

func (s *frameStats) String() string {
	return fmt.Sprintf("%d inputs, %d bytes, %d frames, %d resumed, %d units handed to the engine of %d in the frames", s.inputs.Load(), s.bytes.Load(), s.frames.Load(), s.resumed.Load(), s.engine.Load(), s.units.Load())
}

// htmlWindow is the part of html around byte at, for a failure message.
func htmlWindow(html string, at int) string {
	return html[max(0, min(at, len(html))-60):min(len(html), at+60)]
}

// checkFrames streams code[:cut] for each cut, in order, through a frameChain and compares every frame with a whole run over the same text. It reports the first frame that differs and returns false. It runs on a worker of parallelEach, so it reports with Errorf.
func checkFrames(t *testing.T, r *Registry, id, lang, code string, cuts []int, stats *frameStats) bool {
	chain := frameChain{r: r, lang: lang}
	for _, cut := range cuts {
		text := code[:cut]
		want, err := r.Highlight(text, lang, true)
		got, ok := chain.frame(text)
		if ok != (err == nil) {
			t.Errorf("%s (%s): the frame of %d of %d bytes: a whole run reports %v, the streamed run ok=%v", id, lang, cut, len(code), err, ok)
			return false
		}
		if ok && got != want {
			at := 0
			for at < len(got) && at < len(want) && got[at] == want[at] {
				at++
			}
			t.Errorf("%s (%s): the frame of %d of %d bytes, resumed at byte %d, is not the HTML of a whole run; they differ at HTML byte %d\n got %q\nwant %q", id, lang, cut, len(code), chain.resumedAt, at, htmlWindow(got, at), htmlWindow(want, at))
			return false
		}
	}
	stats.add(&chain, code)
	return true
}

// lineCuts is the offset after every line break of code, and its end.
func lineCuts(code string) []int {
	var cuts []int
	for i := range len(code) {
		if code[i] == '\n' {
			cuts = append(cuts, i+1)
		}
	}
	if len(cuts) == 0 || cuts[len(cuts)-1] != len(code) {
		cuts = append(cuts, len(code))
	}
	return cuts
}

// sampledCuts is n offsets of code chosen by seed, each at the start of a character, and its end, ascending.
func sampledCuts(code string, n int, seed uint64) []int {
	random := rand.New(rand.NewPCG(seed, uint64(len(code))))
	cuts := make([]int, 0, n+1)
	for range n {
		cut := 1 + random.IntN(len(code))
		for cut < len(code) && !utf8.RuneStart(code[cut]) {
			cut++
		}
		cuts = append(cuts, cut)
	}
	cuts = append(cuts, len(code))
	slices.Sort(cuts)
	return slices.Compact(cuts)
}

// everyCut is every offset of code from 1 to its end: every prefix, those that end inside a UTF-8 sequence or between a carriage return and its line feed included.
func everyCut(code string) []int {
	cuts := make([]int, len(code))
	for i := range cuts {
		cuts[i] = i + 1
	}
	return cuts
}

// streamedFrameBytes is the bytes a frame of a streamed answer adds: the scoreboard's md:20000 cell draws a frame every 175 characters (tui md20000FrameChars).
const streamedFrameBytes = 175

// stepCuts is the offsets of code a stream reaches in frames of step bytes, and its end.
func stepCuts(code string, step int) []int {
	var cuts []int
	for at := step; at < len(code); at += step {
		cuts = append(cuts, at)
	}
	return append(cuts, len(code))
}

// chunkCuts is offsets of code that grow by 1 to most bytes at a time, chosen by seed, and its end: the frames of a stream that delivers chunks of uneven sizes, which end inside characters, tokens and modes wherever the sizes put them.
func chunkCuts(code string, most int, seed uint64) []int {
	random := rand.New(rand.NewPCG(seed, uint64(most)))
	var cuts []int
	for at := 0; at < len(code); {
		at = min(at+1+random.IntN(most), len(code))
		cuts = append(cuts, at)
	}
	return cuts
}

// logLanguageFrames logs, for each language of stats, the units its streams handed to the engine for each unit of their texts: 1 is every unit read once.
func logLanguageFrames(t *testing.T, stats *frameStats) {
	t.Helper()
	for _, lang := range slices.Sorted(maps.Keys(stats.byLanguage)) {
		row := stats.byLanguage[lang]
		t.Logf("engine units per unit of text: %-18s %7.3f (%d texts, %d units)", lang, float64(row.engine)/float64(max(1, row.units)), row.texts, row.units)
	}
}

// logLanguagesWithoutASample logs the registered languages the corpus has no sample of, which no frame of the corpus streams.
func logLanguagesWithoutASample(t *testing.T, samples []corpusSample) {
	t.Helper()
	r := NewRegistry()
	r.LoadAllLanguages()
	sampled := map[*Mode]bool{}
	for _, c := range samples {
		if c.Code != "" && r.SupportsLanguage(c.Lang) {
			sampled[r.getLanguage(c.Lang)] = true
		}
	}
	names := r.Languages()
	var missing []string
	for _, name := range names {
		if !sampled[r.getLanguage(name)] {
			missing = append(missing, name)
		}
	}
	t.Logf("%d registered languages, %d without a sample: %s", len(names), len(missing), strings.Join(missing, ", "))
}

// TestStreamedFramesEqualAWholeRun is the property the resumable path stands on: a text that arrives in frames, each frame resumed from the last point an earlier frame offered, has at every frame the HTML of one whole run over the text so far. Every distinct sample of the highlight.js oracle corpus, which has the samples of every registered language but those the test logs, is streamed five times: a frame at every line boundary, a frame at each of 48 sampled characters (inside tokens, strings and comments, wherever the sample puts them), chunks of 1 to 40 and of 1 to 400 bytes, and frames of 175 bytes, for which the test logs a row for each language. Under the race detector every fourth sample runs.
func TestStreamedFramesEqualAWholeRun(t *testing.T) {
	type indexed struct {
		index  int
		sample corpusSample
	}
	samples := distinctSamples(t)
	items := make([]indexed, len(samples))
	for i, sample := range samples {
		items[i] = indexed{i, sample}
	}
	var byLines, bySamples, byChunks frameStats
	bySteps := frameStats{byLanguage: map[string]*languageFrames{}}
	parallelEach(t, items, func() (func(indexed), func(int)) {
		r := NewRegistry()
		r.LoadAllLanguages()
		return func(item indexed) {
			c := item.sample
			if c.Code == "" || raceEnabled && item.index%4 != 0 || !r.SupportsLanguage(c.Lang) {
				return
			}
			if !checkFrames(t, r, c.ID+", a frame per line", c.Lang, c.Code, lineCuts(c.Code), &byLines) {
				return
			}
			h := fnv.New64a()
			h.Write([]byte(c.ID))
			if !checkFrames(t, r, c.ID+", frames at sampled characters", c.Lang, c.Code, sampledCuts(c.Code, 48, h.Sum64()), &bySamples) {
				return
			}
			for _, most := range []int{40, 400} {
				if !checkFrames(t, r, fmt.Sprintf("%s, chunks of up to %d bytes", c.ID, most), c.Lang, c.Code, chunkCuts(c.Code, most, h.Sum64()), &byChunks) {
					return
				}
			}
			checkFrames(t, r, c.ID+", frames of 175 bytes", c.Lang, c.Code, stepCuts(c.Code, streamedFrameBytes), &bySteps)
		}, nil
	})
	t.Logf("a frame per line: %s", &byLines)
	t.Logf("frames at sampled characters: %s", &bySamples)
	t.Logf("chunks of uneven sizes: %s", &byChunks)
	t.Logf("frames of %d bytes: %s", streamedFrameBytes, &bySteps)
	logLanguageFrames(t, &bySteps)
	logLanguagesWithoutASample(t, samples)
	// The floor keeps the corpus exercising resumption; the existing streamed tests hold a quarter of their steps to it.
	if !t.Failed() && byLines.resumed.Load()*10 < byLines.frames.Load() {
		t.Fatalf("only %d of %d line frames resumed: the corpus no longer exercises resumption", byLines.resumed.Load(), byLines.frames.Load())
	}
}

// streamedSnippet is a code block as a model writes one into a fence. A section sign stands for a backtick.
type streamedSnippet struct{ name, lang, code string }

func (s streamedSnippet) text() string { return strings.ReplaceAll(s.code, "§", "`") }

// streamedSnippets are code blocks of the languages most often fenced, with the constructs a frame can end inside: strings, template literals and their substitutions, block and line comments, regular expressions, parameter lists over lines, tags, here documents, blank lines and trailing white space; and modes inside modes: strings with escapes, substitutions inside substitutions, objects inside lists, comments inside documents, here documents with substitutions, a script and a style sheet inside a page, tags inside a template and inside Markdown, and a tag that a template leaves open for the text after it.
var streamedSnippets = []streamedSnippet{
	{"scoreboard block", "typescript", `export function parse1(input: string): Node[] {
	const out: Node[] = [];
	for (let i = 0; i < input.length; i++) {
		if (input.charCodeAt(i) === 0x5b) out.push({ kind: "open", at: i });
	}
	return out;
}
`},
	{"loader", "typescript", `/**
 * Reads the config. See {@link load} for the details you will need.
 * @param path the file
 */
@sealed
export class Loader<T extends object> implements Source<T> {
	private cache = new Map<string, T>();
	constructor(private readonly root: string) {}

	async load(
		path: string,
		{ retries = 3, onRetry }: { retries?: number; onRetry?: (n: number) => void } = {},
	): Promise<T> {
		const url = §${this.root}/${path.replace(/^\/+/, "")}?v=${Date.now()}§;
		// a line comment with a quote ' and a backtick §
		const text = await fetch(url).then((r) => r.text(), (e) => {
			throw new Error('fetch "' + url + "\" failed: " + e);
		});
		return JSON.parse(text, (key, value) =>
			key === "date" ? new Date(value) : value,
		) as T;
	}
}
const view = <T,>(x: T) => x;
const el = <div className="a">{items.map((i) => <li key={i}>{i}</li>)}</div>;
type Pair = [left: string, right: number];
enum Color { Red = 1, Green, Blue }
`},
	{"later text decides", "typescript", `const a = {


	key: 1,
	// c1
	/* c2 */ other: 2,
};
get
  value() { return 1; }
x = y
	/ 2 / z;
foo(
	bar,
)
	=> baz;
label: for (const [k, v] of Object.entries({ a: 1, /* c */ b: 2,
	// trailing
	c: 3 })) { if (v > 1) continue label; }   
call(function
	(a) { return a }, <b>text</b>, <c
	d="e"/>);
`},
	{"module", "javascript", `#!/usr/bin/env node
"use strict";
const fs = require("fs");
function* walk(dir, depth = 0) {
  for (const name of fs.readdirSync(dir)) {
    yield [depth, name];
  }
}
const re = /ab+c[/\]]*$/gi, s = 'it\'s', n = 0x1f + 1e-3 + 12n;
const page = html§<p class="x">${n}</p>§;
const f = async (a,
  b = { c: [1, 2, 3] },
) => {
  return a ? b : null;
};
class A extends B { static #p = 1; get v() { return this.#p; } set v(x) {} }
export default { name: "x", /* note */ run() {}, 'quoted': true,
  nested: { deep: 1 } };
`},
	{"script", "python", `#!/usr/bin/env python3
"""Module docstring that
spans lines with a "quote" inside."""
import os, sys
from typing import List

@dataclass(frozen=True)
class Point(Base):
    x: int = 0
    y: float = 1.5e3

    def norm(self, *args, **kwargs) -> float:
        '''Return the norm.

        >>> Point(1, 2).norm()
        '''
        return (self.x ** 2 + self.y ** 2) ** 0.5

def main(argv: List[str]) -> int:
    name = f"{argv[0]!r:>10} {{literal}} {os.sep}"
    raw = rb'\d+\n'
    for i, a in enumerate(argv):  # trailing comment
        print(f'{i}: {a}', end="\n", file=sys.stderr)
    return 0 if name else None

if __name__ == "__main__":
    sys.exit(main(sys.argv))
`},
	{"package", "go", `// Package demo shows the syntax.
package demo

import (
	"fmt"
	"strings"
)

/* A block
   comment. */
type Shape interface {
	Area() float64
}

type Rect struct{ W, H float64 }

func (r Rect) Area() float64 { return r.W * r.H }

func Describe[T Shape](items []T) (string, error) {
	var b strings.Builder
	for i, it := range items {
		fmt.Fprintf(&b, "%d: %.2f\n", i, it.Area())
	}
	raw := §line one
line "two"§
	ch := make(chan rune, 1)
	go func() { ch <- 'x' }()
	return b.String() + raw + string(<-ch), nil
}
`},
	{"crate", "rust", `//! Crate docs.
use std::collections::HashMap;

/// A point.
#[derive(Debug, Clone, PartialEq)]
pub struct Point<'a, T: Copy> {
    pub x: T,
    name: &'a str,
}

impl<'a, T: Copy + std::fmt::Display> Point<'a, T> {
    /* block /* nested */ comment */
    pub fn show(&self) -> String {
        let raw = r#"a "quoted" \n"#;
        let c = 'x';
        let n = 1_000u64 + 0xff;
        match self.name {
            "" => format!("{}", self.x),
            other => format!("{other}: {} {raw} {c} {n}", self.x),
        }
    }
}

fn main() {
    let mut m: HashMap<&str, i32> = HashMap::new();
    m.insert("a", 1);
    println!("{:?}", m);
}
`},
	{"manifest", "json", `{
  "name": "demo",
  "version": "1.0.0",
  "nested": {
    "list": [1, 2.5, -3e4, true, false, null],
    "empty": {},
    "text": "a \"quoted\" \\ string with \u00e9 and \n"
  },
  "scripts": {
    "build": "tsc -p .",
    "test": "node --test"
  }
}
`},
	{"script", "bash", `#!/usr/bin/env bash
set -euo pipefail
name=${1:-world}
greet() {
  local msg="Hello, $name! It's $(date +%H:%M) ${#name}"
  echo "$msg" 'single $quoted' >&2
}
for f in *.txt; do
  if [[ -f "$f" && $f != skip* ]]; then
    cat <<EOF
file: $f
EOF
  fi
done
case "$name" in
  w*) greet ;;  # comment
  *) echo no ;;
esac
`},
	{"sheet", "css", `@import url("base.css");
:root { --gap: 4px; }
/* comment */
.card > .title:hover::after,
#main a[href^="http"] {
  color: #fff;
  margin: 0 auto calc(var(--gap) * 2);
  background: url(img/a.png) no-repeat;
  font: 12px/1.5 "Helvetica Neue", sans-serif;
}
@media (max-width: 600px) and (orientation: landscape) {
  .card { display: none !important; }
}
`},
	{"page", "xml", `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Demo &amp; test</title>
  <style>
    body { margin: 0; }
  </style>
  <script>
    const x = "</div>" + 1; // comment
  </script>
</head>
<body class="a b" data-x='1'>
  <!-- a comment
       over lines -->
  <p>Text <b>bold</b><br/></p>
  <![CDATA[ raw <text> ]]>
</body>
</html>
`},
	{"query", "sql", `-- comment
SELECT u.id, COUNT(*) AS n, 'it''s' AS s
FROM users u
  LEFT JOIN orders o ON o.user_id = u.id
WHERE u.created_at >= '2024-01-01' /* block */
GROUP BY u.id
HAVING COUNT(*) > 1
ORDER BY n DESC;
INSERT INTO t (a, b) VALUES (1, "x"), (2, NULL);
`},
	{"workflow", "yaml", `# comment
name: demo
on:
  push:
    branches: [main, "release/*"]
env: &defaults
  KEY: 'value'
  TEXT: |
    line one
    line two
jobs:
  build:
    <<: *defaults
    steps:
      - run: echo "hi" && exit 0
      - uses: actions/checkout@v4
`},
	{"document", "markdown", `# Title

Some *emphasis*, **strong** and §code§.

- item [link](http://x.y)
- item

> quote

    indented code

1. one
`},
	{"program", "cpp", `#include <vector>
// comment
template <typename T>
class Box : public Base {
 public:
  explicit Box(T v) : v_(v) {}
  auto get() const -> const T& { return v_; }
 private:
  T v_;
};
int main(int argc, char** argv) {
  std::vector<int> xs{1, 2, 3};
  const char* s = R"x(raw "string")x";
  for (auto& x : xs) { x += 0x10; }
  return 0;
}
`},
	{"class", "java", `package demo;

import java.util.List;

/** Doc. */
public final class Main<T extends Comparable<T>> {
    private static final String S = "a\"b";

    @Override
    public String toString() { return S + 'c' + 1_000L; }

    public static void main(String[] args) throws Exception {
        List<String> xs = List.of("a", "b");
        for (String x : xs) { System.out.println(x); }
    }
}
`},
	{"module", "ruby", `# frozen_string_literal: true
require "json"
module Demo
  class Greeter < Base
    attr_reader :name
    def initialize(name = "world") = @name = name
    def greet
      text = <<~EOS
        Hello, #{name}!
      EOS
      puts text, 'single', :sym, %w[a b], /re+/i
    end
  end
end
`},
	// Texts whose earlier tokens a later line decides: Apache's bracket after a line break, QML's brace or colon after blank lines, a Markdown heading underlined on the next line.
	{"bracket after a line", "apache", "x\nfoo\n[ y\nz\n"},
	{"bracket after a line with a space", "apache", "x\nfoo \n[ y\nz\n"},
	{"bracket after a number", "apache", "x\n1\n[ y\nz\n"},
	{"bracket after a name", "apache", "x\n@foo\n[ y\nz\n"},
	{"bracket after a dotted name", "apache", "x\nFoo.bar\n[ y\nz\n"},
	{"brace after blank lines", "qml", "x\nFoo.bar\n\n{ y\nz\n"},
	{"colon after blank lines", "qml", "x\nFoo.bar \n\n: y\nz\n"},
	{"colons after blank lines", "qml", "x\nFoo.bar\t\n\n:: y\nz\n"},
	{"brace after a line of spaces", "qml", "x\nFoo.bar\n \n\n{ y\nz\n"},
	{"underlined headings", "markdown", "Some *emphasis* in a heading\n============\n\ntext with `code` here\n----\n\nlast *words*\n"},
	{"page", "php", `<?php
namespace Demo;
/** Doc */
final class A extends B implements C {
    public function f(int $x = 1, ?string $s = null): array {
        $t = "v: {$x} $s" . 'q' . <<<EOT
        here $x
        EOT;
        return [$x => $t, 'k' => 1.5];
    }
}
?>
<p>html</p>
`},
	{"documents in a list", "json", `[
  {
    "id": 1,
    "name": "a \"quoted\" name \\ with \u00e9",
    "tags": ["x", "y", ["deep", [1, 2.5e3, null]]],
    "meta": {"ok": true, "none": null, "list": [], "obj": {}}
  },
  // a comment between documents
  {
    "id": 2,
    "path": "C:\\dir\\file",
    "nested": {"a": {"b": {"c": [{"d": "e"}, {"f": [false]}]}}},
    /* a block comment
       over lines */
    "last": -0.5
  }
]
`},
	{"substitutions", "python", `def render(items, sep=", "):
    """Join the items.

    The second paragraph has a \"quote\" and a \\ backslash.
    """
    head = f"{len(items):>4} items: {sep.join(f'{i!r}' for i in items)} {{braces}}"
    body = f'''first {items[0]}
second {items[1] if len(items) > 1 else "none"}
third \N{BULLET} {{literal}}'''
    return head + "\n" + body, b"\x00\xff", r"raw \d+ {not} a field"
`},
	{"strings and a comment", "go", `package demo

/* A block comment that the frames end inside of: you will see
   the words it marks, and the lines it runs over. */
var (
	escaped = "tab\t quote\" unicode\u00e9 \\"
	raw     = §first line
second \n line
§ + "tail"
	char = '\''
)
`},
	{"here documents", "bash", `cat <<EOF
first $name line
second $(date "+%H:%M") line
EOF
msg="outer $(echo "inner ${name:-"default value"}" | tr a-z A-Z) end"
cat <<-'RAW'
	$not expanded
	RAW
echo "after ${#msg} \"escaped\" $((1 + 2))"
`},
	{"nested rules", "css", `@media (min-width: 600px) and (max-width: 900px) {
  .card > a[href^="http://"]:not(.x)::after {
    content: "\201C quoted \" text";
    background: url("img/a b.png") no-repeat, linear-gradient(to right, rgba(0, 0, 0, .5), #fff);
    margin: 0 auto !important; /* a comment
    over lines */
  }
  @supports (display: grid) {
    .grid { display: grid; grid-template-columns: repeat(3, 1fr); }
  }
}
`},
	{"embedded script and style", "xml", `<div class="a"
     data-x='1 > 0'
     hidden>
  <style media="screen">
    .a { color: red; }
    .b::after { content: "</p>"; }
  </style>
  <script type="text/javascript">
    const s = §line ${a + §inner ${b}§} end
    second§;
    if (a < b && b > c) { run("</div>"); }
  </script>
  <p>after <b>bold</b></p>
</div>
`},
	{"a tag the template continues", "erb", `<a href="<%= url %>"
   class="x">link</a>
<p>one</p>
<% items.each do |item| %>
  <li title="<%= item.name %>"><%= item %></li>
<% end %>
<p>two</p>
<p>three</p>
`},
	{"a tag the template continues", "django", `<a href="{{ url }}"
   class="x">{{ label }}</a>
<p>one</p>
{% for item in items %}
  <li title="{{ item.name }}">{{ item }}</li>
{% endfor %}
<p>two</p>
<p>three</p>
`},
	{"a tag the next template continues", "javascript", `const open = html§<div class="a§;
const one = 1;
const two = 2;
const three = 3;
const rest = html§ b">text</div>§;
const four = 4;
const five = 5;
`},
	{"embedded code and tags", "markdown", `Intro with <span class="x">inline *tag*</span> text.

<div align="center">
  <img src="a.png" alt="A">
</div>

§§§js
const a = §t ${x}§;
§§§

1. item with §code§ and [a link](http://x.y "title")
   continued line

Heading
-------
`},
	{"template literals", "javascript", `const page = html§
  <ul class="${cls}">
    ${items.map((item) => §<li>${item.name + §(${item.id})§}</li>§).join("")}
  </ul>
§;
const s = 'it\'s \
continued', t = §a ${b} c§;
/** A comment
 * @param {string} a the "name"
 */
`},
	{"template literal types", "typescript", `type Route<T extends string> = §/${T}/${number}§;
const text = §first ${one(§inner ${two}§)}
second line ${three /* comment */} end§;
function f<A, B = A[]>(a: A, ...rest: B[]): asserts a is A {
	return;
}
`},
	{"strings and comments", "sql", `SELECT 'it''s', "quoted ""name""", 'esc\'aped'
FROM t /* a block
comment */ WHERE a = 'two
lines' -- trailing
  AND b IN (1, 2.5, 'x');
`},
	{"interpolation", "ruby", `text = <<~EOS
  first #{name} line
  second line
EOS
s = "outer #{items.map { |i| "inner #{i}" }.join(", ")} end"
r = %r{a/b#{c}}i
`},
}

// snippetVariants are the texts a snippet is streamed as: as written; with carriage returns before its line feeds; with one and then two backticks on a line after it, the start of the fence that closes it; and with a line of characters outside ASCII after it (two and three byte sequences, a surrogate pair, a no-break space, which a white-space pattern matches, and U+2028, a line terminator that is not a line feed).
func snippetVariants(s streamedSnippet) []streamedSnippet {
	text := s.text()
	return []streamedSnippet{
		{s.name, s.lang, text},
		{s.name + ", CRLF", s.lang, strings.ReplaceAll(text, "\n", "\r\n")},
		{s.name + ", closing fence", s.lang, text + "``"},
		{s.name + ", outside ASCII", s.lang, text + "\tnaïve café 日本語 😀\u00a0=\u00a0\"x\u2028y\" é\n"},
	}
}

// TestStreamedFramesEqualAWholeRunAtEveryPrefix streams each snippet variant one byte at a time: every prefix is a frame, so a frame ends inside every token, string, comment, substitution and UTF-8 sequence of it, and after every line. It then streams the variant in chunks of 1 to 3, to 17 and to 60 bytes, whose frames resume from points several frames old. Under the race detector every fourth variant runs.
func TestStreamedFramesEqualAWholeRunAtEveryPrefix(t *testing.T) {
	type indexed struct {
		index   int
		snippet streamedSnippet
	}
	var items []indexed
	for _, s := range streamedSnippets {
		for _, variant := range snippetVariants(s) {
			items = append(items, indexed{len(items), variant})
		}
	}
	var stats, chunks frameStats
	parallelEach(t, items, func() (func(indexed), func(int)) {
		r := NewRegistry()
		r.LoadAllLanguages()
		return func(item indexed) {
			s := item.snippet
			if raceEnabled && item.index%4 != 0 {
				return
			}
			if !r.SupportsLanguage(s.lang) {
				t.Errorf("%s: no language %s", s.name, s.lang)
				return
			}
			if !checkFrames(t, r, s.name+", every prefix", s.lang, s.code, everyCut(s.code), &stats) {
				return
			}
			for _, most := range []int{3, 17, 60} {
				if !checkFrames(t, r, fmt.Sprintf("%s, chunks of up to %d bytes", s.name, most), s.lang, s.code, chunkCuts(s.code, most, uint64(item.index)), &chunks) {
					return
				}
			}
		}, nil
	})
	t.Logf("every prefix: %s", &stats)
	t.Logf("chunks of uneven sizes: %s", &chunks)
	if !t.Failed() && stats.resumed.Load()*10 < stats.frames.Load() {
		t.Fatalf("only %d of %d frames resumed: the snippets no longer exercise resumption", stats.resumed.Load(), stats.frames.Load())
	}
}
