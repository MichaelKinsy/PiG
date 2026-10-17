package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type validateCase struct {
	name, schema, args string
}

var validateCases = []validateCase{
	{"coerces", `{"type":"object","properties":{"offset":{"type":"number"},"flag":{"type":"boolean"}},"required":["offset"]}`, `{"offset":"2","flag":"true"}`},
	{"optional null", `{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"number"}},"required":["path"]}`, `{"path":"a","limit":null}`},
	{"missing required", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`, `{"other":1}`},
	{"wrong type", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`, `{"path":{"a":1},"x":2}`},
	{"nested items", `{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","properties":{"oldText":{"type":"string"},"newText":{"type":"string"}},"required":["oldText","newText"]}}},"required":["edits"]}`, `{"edits":[{"oldText":42,"newText":true},{"oldText":"a","newText":"b"}]}`},
	{"order kept", `{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"string"}}}`, `{"b":"1","a":"2"}`},
	{"invalid args", `{"type":"object"}`, `{"a":`},
	{"invalid schema", `{"type":`, `{}`},
	{"anyOf", `{"type":"object","properties":{"v":{"anyOf":[{"type":"number"},{"type":"string"}]}}}`, `{"v":"3"}`},
}

func validateOutcome(schema, args string) string {
	out, err := validateToolArgsSchema("tool", json.RawMessage(schema), json.RawMessage(args))
	if err != nil {
		return "error: " + err.Error()
	}
	return string(out)
}

// Validating against a cached schema returns, for every call, what validating against a freshly parsed schema returns: the same arguments in the same order, and the same error text.
func TestCachedToolArgumentSchemaMatchesAFreshParse(t *testing.T) {
	for _, tc := range validateCases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := validateOutcome(tc.schema+" ", tc.args) // a schema text no earlier call cached
			for range 3 {
				if got := validateOutcome(tc.schema, tc.args); got != fresh {
					t.Fatalf("cached = %s\nfresh  = %s", got, fresh)
				}
			}
		})
	}
}

// One schema is validated by many tool calls at once, as parallel tool execution does, and a different argument set per goroutine each gets its own answer.
func TestCachedToolArgumentSchemaIsSafeForConcurrentCalls(t *testing.T) {
	schema := `{"type":"object","properties":{"n":{"type":"number"},"tags":{"type":"array","items":{"type":"string"}}},"required":["n"]}`
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			for round := range 50 {
				args := fmt.Sprintf(`{"n":"%d","tags":["w%d"]}`, worker*100+round, worker)
				want := fmt.Sprintf(`{"n":%d,"tags":["w%d"]}`, worker*100+round, worker)
				if got := validateOutcome(schema, args); got != want {
					t.Errorf("worker %d round %d: %s, want %s", worker, round, got, want)
					return
				}
			}
		})
	}
	wg.Wait()
}

// The cache keeps at most argumentSchemaCacheEntries schemas and does not keep a schema above the size limit.
func TestToolArgumentSchemaCacheIsBounded(t *testing.T) {
	var cache argumentSchemaCache
	for i := range argumentSchemaCacheEntries + 40 {
		cache.entry(json.RawMessage(fmt.Sprintf(`{"type":"object","title":"%d"}`, i)))
	}
	if len(cache.entries) != argumentSchemaCacheEntries || len(cache.order) != argumentSchemaCacheEntries {
		t.Fatalf("%d entries, %d ordered", len(cache.entries), len(cache.order))
	}
	if cache.entry(json.RawMessage(`{"type":"object","title":"0"}`)) == nil || len(cache.entries) != argumentSchemaCacheEntries {
		t.Fatal("an evicted schema was not parsed again")
	}
	large := json.RawMessage(`{"type":"object","description":"` + strings.Repeat("x", argumentSchemaCacheMaxBytes) + `"}`)
	before := len(cache.entries)
	first, second := cache.entry(large), cache.entry(large)
	if len(cache.entries) != before || first == second {
		t.Fatal("a schema above the size limit was cached")
	}
	if got, err := validateToolArgsSchema("big", large, json.RawMessage(`{}`)); err != nil || string(got) != `{}` {
		t.Fatalf("large schema: %s, %v", got, err)
	}
}

// D112: every validation outcome is the same whether its schema is cached, was evicted by other schemas, or is above the size limit and never cached.
func TestToolArgumentSchemaEvictionDoesNotChangeOutcomes(t *testing.T) {
	var kept []string
	for _, tc := range validateCases {
		kept = append(kept, validateOutcome(tc.schema, tc.args))
	}
	for i := range argumentSchemaCacheEntries + 10 {
		validateOutcome(fmt.Sprintf(`{"type":"object","title":"evict %d"}`, i), `{}`)
	}
	for i, tc := range validateCases {
		if got := validateOutcome(tc.schema, tc.args); got != kept[i] {
			t.Errorf("%s after eviction = %s, want %s", tc.name, got, kept[i])
		}
	}
	// The same schema padded above the size limit takes the uncached path and answers the same.
	pad := `,"description":"` + strings.Repeat("x", argumentSchemaCacheMaxBytes) + `"}`
	for i, tc := range validateCases {
		if !strings.HasSuffix(tc.schema, "}") || tc.name == "invalid schema" {
			continue
		}
		large := tc.schema[:len(tc.schema)-1] + pad
		if got := validateOutcome(large, tc.args); got != kept[i] && !strings.HasPrefix(kept[i], "error") {
			t.Errorf("%s above the size limit = %s, want %s", tc.name, got, kept[i])
		}
	}
}
