package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzExtractColorKeyOrderMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SameResult(t, data, "extractColorKeyOrder", extractColorKeyOrder(data), extractColorKeyOrderV1(data))
	})
}

func FuzzRawObjectKeysMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		jsonseed.SkipJSKeys(t, data)
		jsonseed.SameResult(t, data, "rawObjectKeys", rawObjectKeys(data), rawObjectKeysV1(data))
	})
}

func FuzzToolCallArgEntriesMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		got, gotOK := toolCallArgEntries(data)
		want, wantOK := toolCallArgEntriesV1(data)
		jsonseed.SameResult(t, data, "toolCallArgEntries", []any{got, gotOK}, []any{want, wantOK})
	})
}

func BenchmarkTuiJSONReaders(b *testing.B) {
	doc := jsonseed.Typical()
	b.Run("extractColorKeyOrder", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { extractColorKeyOrderV1(d) }, func(d []byte) { extractColorKeyOrder(d) })
	})
	b.Run("rawObjectKeys", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { rawObjectKeysV1(d) }, func(d []byte) { rawObjectKeys(d) })
	})
	b.Run("toolCallArgEntries", func(b *testing.B) {
		jsonseed.Bench(b, doc, func(d []byte) { toolCallArgEntriesV1(d) }, func(d []byte) { toolCallArgEntries(d) })
	})
}
