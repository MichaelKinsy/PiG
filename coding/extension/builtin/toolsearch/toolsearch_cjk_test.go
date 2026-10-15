package toolsearch_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// earendil-works/pi#10816 (D173): Pi's tokenizer splits on /[^a-z0-9]+/, so every Chinese, Japanese and Korean character is dropped, `tokenize("记录")` is [] and a query
// in those scripts never matches a tool whose metadata holds the same words. PiG makes each such character a term; text without them tokenizes as in Pi.
func TestTokenizeKeepsCJKCharactersAsTerms(t *testing.T) {
	for _, test := range []struct {
		text string
		want []string
	}{
		{"记录", []string{"记", "录"}},
		{"search_notes 搜索记录", []string{"search", "note", "搜", "索", "记", "录"}},
		// The prolonged sound mark is script Common, not Katakana, so it splits a term as in Pi.
		{"ノート検索", []string{"ノ", "ト", "検", "索"}},
		{"노트", []string{"노", "트"}},
		{"readFile 読む", []string{"read", "file", "読", "む"}},
		// Without those scripts the result is Pi's: accented letters still split a word.
		{"café résumé", []string{"caf", "r", "sum"}},
		{"Привет", []string{}},
		{"", []string{}},
	} {
		if got := toolsearch.Tokenize(test.text); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Tokenize(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}

func TestBm25RankerFindsAChineseQueryInChineseToolMetadata(t *testing.T) {
	documents := []toolsearch.Document{
		{Name: "search_notes", Text: "search_notes 搜索记录"},
		{Name: "send_mail", Text: "send_mail 发送邮件"},
	}
	if got, want := names(toolsearch.NewBm25Ranker().Rank("记录", documents, 8)), []string{"search_notes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("记录: %q, want %q", got, want)
	}
	if got, want := names(toolsearch.NewBm25Ranker().Rank("邮件", documents, 8)), []string{"send_mail"}; !reflect.DeepEqual(got, want) {
		t.Errorf("邮件: %q, want %q", got, want)
	}
}
