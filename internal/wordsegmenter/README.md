# Word segmentation

This package supplies the shared word segments for Input and Editor. It applies Unicode 17 UAX #29 rules from `github.com/clipperhouse/uax29/v2/words`, ICU 78.3 word-rule tailoring, and ICU's Chinese/Japanese, Thai, Lao, Khmer and Burmese dictionary engines. Offsets returned to callers count UTF-16 units. Segments retain their original spelling, including unpaired surrogate units.

## Rule and dictionary behavior

`word_rules.go` implements ICU `word.txt`'s Complex_Context addition to ALetterPlus, CJK/Hangul exclusions, Han exclusion from Extend, CJK/Hangul chaining, and rule-status selection. Same-byte-width class representatives feed the tailored character classes to the UAX scanner. They never replace the text exposed to callers or dictionary lookups.

`segments.go` applies each engine only within its handled character span. Dictionary engines omit the span's final boundary. The containing rule span provides the outer endpoints and word-like status. In particular, an engine switch does not create a new word stop by itself. Non-dictionary combining marks terminate an engine span without necessarily terminating its containing rule span.

`segments.go` also ports `DictionaryCache::populateDictionary` engine selection. A rule span of one UTF-16 unit is not subdivided. An engine span starts only at an ICU `$dictionary` character, so U+FF9E and U+FF9F can continue a CJK span but cannot start one. Each `Segments` call models one break iterator's engine stack. `UnhandledEngine` takes a `$dictionary` character without an engine, such as U+309B, a Hangul syllable, or Tai Tham. It claims that character's whole Script value, replacing any previous claim, and consumes the following run of that script without breaks. It is searched before the process factory, so after U+309B claims Common, U+30FC and U+FF70 go to it until the CJK engine is on the stack. ICU's process factory caches engines for the life of the process. Pig models the state after the factory has loaded the CJK engine. A fresh Pi process that has not yet loaded it treats a first U+30FC or U+FF70 span start as unhandled; for example, its first `findWordBackward("ー你好", 3)` returns 0, and later calls return 1, as Pig does.

`cjk.go` translates `CjkBreakEngine::divideUpDictionaryRange` in word mode. It preserves NFKC boundary mapping, dictionary costs, the 20-code-point candidate limit, 255 unknown-character cost, Katakana cost table, and shortest-path tie order. It does not use phrase-breaking heuristics. Hangul syllables stay together under the word rules; ICU has no dictionary entries or unknown-character splits for them.

`sea.go` translates `DictionaryBreakEngine`, `PossibleWord`, and the four Southeast Asian engines. It preserves the three-word candidate cache and backtracking order, 20-candidate limit, prefix length including the mismatching code point, root/prefix combination thresholds, script-specific begin/end/mark sets, Thai suffix handling, and minimum span rules. Thai requires more than four code points; the other engines require at least four. These engines handle BMP characters, so their code-point positions equal UTF-16 positions. The generator verifies the three-byte UTF-8 invariant used to convert their boundaries back to bytes.

The scalar word-like set in `word_like_data.go` comes from the pinned ICU rule-status oracle, not general Letter/Number categories. For example, superscripts remain non-word, while circled Latin letters remain word-like. Regenerate it with:

```sh
node internal/wordsegmenter/generate_word_like.mjs
gofmt -w internal/wordsegmenter/word_like_data.go
```

## Pinned sources

ICU sources are under <https://github.com/unicode-org/icu/tree/release-78.3/icu4c/source>. The engine source is `common/dictbe.cpp` (SHA256 `7ff35464a40669eb77ef4931f0fe6a041392f12603f07908af309dda3e22a022`). Rule tailoring comes from `data/brkitr/rules/word.txt` (SHA256 `8c623551556473c97f32a1ecc22716c4d73fbf71b8dc500a4b461302ca146171`). `common/dictionarydata.cpp` defines prefix-match semantics, `common/rbbi_cache.cpp` defines engine-span and rule-status ownership, and `common/rbbi.cpp` and `common/brkeng.cpp` define engine selection and `UnhandledEngine`.

Dictionary sources are in `data/brkitr/dictionaries/`:

| File | SHA256 | Embedded index bytes |
|---|---|---:|
| `cjdict.txt` | `e73fd72048981d0cc13e9dc436a7eaba07ffb6eff58c8a59dc75c1df746663a0` | 4,212,243 |
| `thaidict.txt` | `3166abde40c0f44ab91c28f5ce96d7d1472cb7882e1c0bda0a72f8f69dba4274` | 260,897 |
| `laodict.txt` | `3c876934a3fa81031d2333525eafaca6a7c9f842e3b98f18c38880420afb5d36` | 335,860 |
| `khmerdict.txt` | `87bee2d17cd5148aa36957eb05409eefc124de8ad519b81b789298ef3e60b5d9` | 974,071 |
| `burmesedict.txt` | `61d8abc3d9102b2f9bf0c9f44db0d7ab89b18172d8cd26832e4c83174bd8673b` | 537,893 |

The Unicode property files are from <https://www.unicode.org/Public/17.0.0/ucd/>. `generate_sea_dictionary.py` pins and verifies the hashes of `LineBreak.txt`, `Scripts.txt`, `auxiliary/WordBreakProperty.txt`, and `auxiliary/GraphemeBreakProperty.txt`.

## Regeneration

Download the pinned dictionaries to a temporary directory. Put the four Unicode property files in that directory by basename. Run:

```sh
python3 internal/wordsegmenter/generate_dictionary.py /path/to/cjdict.txt
python3 internal/wordsegmenter/generate_sea_dictionary.py /path/to/source-directory
gofmt -w internal/wordsegmenter/rule_data.go
```

The CJK index has one current unversioned shape: a little-endian uint32 entry count, `count + 1` uint32 offsets, `count` byte weights, and concatenated UTF-8 words in byte-lexical order.

The Southeast Asian indexes have one current unversioned shape: a little-endian uint32 entry count, `count + 1` uint32 offsets, and concatenated offset-transformed word bytes in lexical order. They preserve the byte transforms in ICU's `data/BUILDRULES.py`, including the ZWNJ/ZWJ reserved bytes. They need no weights. The four indexes total 2,108,721 bytes.

Lookup borrows the embedded strings. It narrows a sorted prefix range without constructing a runtime dictionary map or decompressing the data on the input loop. `PossibleWord` caches have fixed size. Per-call temporary storage scales with the supplied text, not Session history. No worker, retained cache, IPC, network call or file descriptor is created.

The generators copy the complete dictionary notices to `LICENSES/LicenseRef-ICU-CJK.txt` and `LICENSES/LicenseRef-ICU-SEA.txt`. See those files, `LICENSES/Unicode-3.0.txt`, `REUSE.toml`, and `THIRD_PARTY_NOTICES.md` for redistribution terms.

## Differential evidence

`testdata/sea-icu78.json` contains 7,831 texts and 201,744 directional cursor probes. It records exact segment text, UTF-16 indexes, word-like flags, and the result of Pi's actual `findWordBackward` and `findWordForward` at every UTF-16 cursor. The corpus includes each script, dictionary samples and combinations, unknown and truncated runs, minimum spans, combining marks, Thai suffixes, punctuation, spaces, digits, emoji, script switches, CJK/Hangul tailoring, rule-status tails, and engine selection. Its generator requires Node 26.7.0 / ICU 78.3 and Pi 0.87.1.

```sh
node internal/wordsegmenter/generate_sea_fixture.mjs /path/to/source-directory
# Optional second argument: another pinned pi-tui/dist directory.
go test ./internal/wordsegmenter ./tui -run TestICUSoutheastAsian
```

The original implementation fails `TestICUSoutheastAsianSegments` and `TestICUSoutheastAsianWordNavigation` in all four scripts. A separate red probe caught CJK/Hangul rule-class exclusions, false engine-end boundaries at combining marks, and rule-status propagation. A random differential probe against the pinned Node segmenter then found engine-selection differences: a span started at U+FF9E, and `UnhandledEngine` state was not modeled. The `engine-selection` cases fail the previous implementation and each compiling mutation of those rules. The final corpus has zero mismatches. An engine-disabled compiling mutation also fails both tests and all five Southeast Asian Editor cases in `tui-components/20-editor-word-and-paste-segments`. That scenario retains `output_equal = true` and three paired runs, including word movement, deletion, yank, paste markers, UTF-16 cursor state and rendered bytes.

On linux/amd64 with Go 1.27.1, equivalent `go build -trimpath -ldflags='-s -w' ./cmd/pig` artifacts grow from 53,108,999 to 55,238,919 bytes: +2,129,920 bytes (4.01%). `BenchmarkSoutheastAsianWordNavigation` measures ordinary and 128/4096-repeat inputs through the production helper. A CPU/allocation profile attributes dictionary CPU work to prefix lookup and most allocation volume to existing UTF-16 slicing plus the tailored scanner buffer. The measured ordinary calls take about 6–11 µs; the largest 4096-repeat cases take about 12–24 ms and allocate 1.6–3.5 MB. These are workload measurements, not a fixed latency guarantee.
