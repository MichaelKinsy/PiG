package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkToolCardArgumentUpdates is a write call whose content streams in 4000 deltas to 100 KB. The card receives the argument text received so far on each delta, which is valid JSON only once the last delta arrives ("complete") or when a provider sends whole objects ("whole").
func BenchmarkToolCardArgumentUpdates(b *testing.B) {
	updates := make([]json.RawMessage, 4000)
	prefixes := make([]json.RawMessage, 4000)
	var content strings.Builder
	for i := range updates {
		content.WriteString("line " + strconv.Itoa(i) + " of the file\\n")
		updates[i] = json.RawMessage(`{"path":"/work/project/src/file.go","content":"` + content.String() + `"}`)
	}
	final := string(updates[len(updates)-1])
	for i := range prefixes {
		prefixes[i] = json.RawMessage(final[:len(final)*(i+1)/len(prefixes)])
	}
	for _, shape := range []struct {
		name    string
		updates []json.RawMessage
	}{{"whole", updates}, {"complete", prefixes}} {
		for _, tool := range []string{"write", "bash", "mcp_unknown"} {
			if shape.name == "complete" && tool != "write" {
				continue
			}
			benchToolCard(b, shape.name+"/"+tool, tool, shape.updates)
		}
	}
	// The interactive delta handler also hands each delta's text to the definition's renderers: read, write and edit cards always have a definition.
	b.Run("complete/write-definition", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work/project")
			card.SetDefinition(&ToolDefinitionRenderers{}, nil)
			for _, args := range prefixes {
				card.SetDefinitionArgs(args)
				card.UpdateArgs(args)
			}
		}
	})
}

func benchToolCard(b *testing.B, name, tool string, updates []json.RawMessage) {
	{
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				card := NewToolExecutionComponent(tool, "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work/project")
				for _, args := range updates {
					card.UpdateArgs(args)
				}
			}
		})
	}
}

// BenchmarkToolCardDefinitionArgumentUpdates is the interactive delta path of a card drawn through a tool definition, as every built-in tool's card is: each delta passes the argument text received so far to SetDefinitionArgs and UpdateArgs.
func BenchmarkToolCardDefinitionArgumentUpdates(b *testing.B) {
	var content strings.Builder
	for i := range 4000 {
		content.WriteString("line " + strconv.Itoa(i) + " of the file\\n")
	}
	final := `{"path":"/work/project/src/file.go","content":"` + content.String() + `"}`
	prefixes := make([]json.RawMessage, 4000)
	for i := range prefixes {
		prefixes[i] = json.RawMessage(final[:len(final)*(i+1)/len(prefixes)])
	}
	b.ReportAllocs()
	for b.Loop() {
		card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work/project")
		card.SetDefinition(&ToolDefinitionRenderers{}, nil)
		for _, args := range prefixes {
			card.SetDefinitionArgs(args)
			card.UpdateArgs(args)
		}
	}
}
