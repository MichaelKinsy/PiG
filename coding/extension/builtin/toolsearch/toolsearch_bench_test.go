package toolsearch_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// benchmarkTools is a tool set of the size of several connected MCP servers: namespaced names, descriptions of a few sentences and schemas with described properties. With
// nonASCII, one tool in five has a description and a property description in another script, which Tokenize segments with the ICU word rules instead of splitting at spaces.
func benchmarkTools(count int, nonASCII bool) []toolsearch.Document {
	documents := make([]toolsearch.Document, count)
	for i := range documents {
		description := fmt.Sprintf("Read item %d of the repository, filtered by state, labels or assignee. Returns the item with its comments, the author and the timeline of changes.", i)
		property := "The number of the issue or pull request to read, as shown in the list."
		if nonASCII && i%5 == 0 {
			description += " Lire l'élément du dépôt, résumé des cafés. Поиск документов. 搜索仓库问题。文書を検索。ค้นหาเอกสาร。"
			property = "问题的编号，文書を検索，Поиск документов"
		}
		tool := extension.ToolInfo{
			Name:        fmt.Sprintf("mcp__server%d__read_item_%d", i%12, i),
			Description: description,
			Parameters:  []byte(`{"type":"object","properties":{"number":{"type":"number","description":"` + property + `"},"repo":{"type":"string","description":"owner/name of the repository"},"state":{"type":"string","description":"open, closed or all"}}}`),
		}
		documents[i] = toolsearch.CreateDocument(tool, &extension.ToolNamespace{Name: fmt.Sprintf("mcp__server%d", i%12), Description: "Repository server", Instructions: strings.Repeat("Use owner/repo for repositories. ", 6)})
	}
	return documents
}

// BenchmarkBm25RankerRank is one tool_search call over a large tool set: every document is tokenized per call, as Pi's Bm25Ranker does.
func BenchmarkBm25RankerRank(b *testing.B) {
	for _, set := range []struct {
		name     string
		count    int
		nonASCII bool
	}{{"ascii-250", 250, false}, {"mixed-scripts-250", 250, true}, {"ascii-1000", 1000, false}, {"mixed-scripts-1000", 1000, true}} {
		documents := benchmarkTools(set.count, set.nonASCII)
		ranker := toolsearch.NewBm25Ranker()
		b.Run(set.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ranker.Rank("read the issue comments of a repository", documents, 8)
			}
		})
	}
}
