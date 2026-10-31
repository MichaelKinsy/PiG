package schema_test

// Ports packages/schema/test/bench/corpus.ts: a deterministic corpus of distinct tool-like JSON Schemas with one valid value each: random property names, descriptions, leaf constraints, enums, nested objects, arrays and some $defs/$ref. No two schemas are copies.

import (
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// corpusItem is a schema and a value it accepts.
type corpusItem struct {
	schema *schema.Object
	value  any
}

var syllables = []string{"ka", "lo", "mi", "re", "su", "ta", "ne", "vo", "pi", "da", "ru", "xe", "bo", "fi", "gu", "ha", "jo", "ze"}

var corpusPatterns = [][2]string{
	{"^[a-z0-9-]+$", "a-1"},
	{"^[A-Za-z0-9._-]+$", "A.b_c"},
	{`^\d{4}-\d{2}-\d{2}$`, "2026-10-31"},
	{"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", "01890a5d-ac96-774b-bcce-b302099a8057"},
	{"^#[0-9a-f]{6}$", "#00ff00"},
}

// mulberry32 is the generator of corpus.ts, with JavaScript's int32 arithmetic.
func mulberry32(seed int32) func() float64 {
	state := seed
	return func() float64 {
		state += 0x6d2b79f5
		t := (state ^ int32(uint32(state)>>15)) * (1 | state)
		t = (t + (t^int32(uint32(t)>>7))*(61|t)) ^ t
		return float64(uint32(t^int32(uint32(t)>>14))) / 4294967296
	}
}

// corpus returns count distinct schemas, the same for the same seed.
func corpus(count int, seed int32) []corpusItem {
	random := mulberry32(seed)
	integer := func(lo, hi int) int { return lo + int(math.Floor(random()*float64(hi-lo+1))) }
	pick := func(list []string) string { return list[int(math.Floor(random()*float64(len(list))))] }
	word := func() string {
		n := integer(2, 4)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = pick(syllables)
		}
		return strings.Join(parts, "")
	}
	sentence := func() string {
		n := integer(4, 12)
		words := make([]string, n)
		for i := range words {
			words[i] = word()
		}
		return strings.Join(words, " ") + "."
	}

	var object func(depth, n int) (*schema.Object, *schema.Object)
	leaf := func(depth int) (*schema.Object, any) {
		r := random()
		switch {
		case r < 0.3:
			s := chordjson.ObjectOf("type", "string", "description", sentence())
			return s, word()
		case r < 0.4:
			min := integer(0, 3)
			max := float64(min + integer(5, 200))
			return chordjson.ObjectOf("type", "string", "minLength", float64(min), "maxLength", max, "description", sentence()), strings.Repeat("x", min+1)
		case r < 0.47:
			p := corpusPatterns[int(math.Floor(random()*float64(len(corpusPatterns))))]
			return chordjson.ObjectOf("type", "string", "pattern", p[0], "description", sentence()), p[1]
		case r < 0.55:
			n := integer(2, 5)
			values := make([]any, n)
			for i := range values {
				values[i] = word()
			}
			return chordjson.ObjectOf("type", "string", "enum", values, "description", sentence()), values[0]
		case r < 0.65:
			min := integer(0, 10)
			max := float64(min + integer(10, 10000))
			return chordjson.ObjectOf("type", "integer", "minimum", float64(min), "maximum", max, "description", sentence()), float64(min + 1)
		case r < 0.72:
			s := chordjson.ObjectOf("type", "number", "description", sentence())
			return s, random() * 100
		case r < 0.8:
			s := chordjson.ObjectOf("type", "boolean", "description", sentence())
			return s, random() < 0.5
		case r < 0.88:
			maxItems := float64(integer(1, 50))
			s := chordjson.ObjectOf("type", "array", "items", chordjson.ObjectOf("type", "string"), "maxItems", maxItems, "description", sentence())
			return s, []any{word()}
		}
		if depth < 2 {
			s, v := object(depth+1, integer(2, 5))
			return s, v
		}
		return chordjson.ObjectOf("type", "string", "description", sentence()), word()
	}
	object = func(depth, n int) (*schema.Object, *schema.Object) {
		properties := &schema.Object{}
		value := &schema.Object{}
		var required []any
		for i := range n {
			name := word()
			if i > 0 {
				name += strconv.Itoa(i)
			}
			s, v := leaf(depth)
			properties.Set(name, s)
			if random() < 0.6 {
				required = append(required, name)
				value.Set(name, v)
			} else if random() < 0.5 {
				value.Set(name, v)
			}
		}
		s := chordjson.ObjectOf("type", "object", "properties", properties, "required", requiredOrEmpty(required))
		if random() < 0.5 {
			s.Set("additionalProperties", false)
		}
		return s, value
	}

	items := make([]corpusItem, 0, count)
	for range count {
		s, value := object(0, integer(2, 10))
		properties := s.Value("properties").(*schema.Object)
		if random() < 0.2 {
			// Move one nested object into $defs and reference it, as MCP servers built with zod often do.
			for _, key := range properties.Keys() {
				if properties.Value(key).(*schema.Object).Value("type") == "object" {
					name := word() + "Def"
					s.Set("$defs", chordjson.ObjectOf(name, properties.Value(key)))
					properties.Set(key, chordjson.ObjectOf("$ref", "#/$defs/"+name))
					break
				}
			}
		}
		s.Set("description", sentence())
		items = append(items, corpusItem{schema: s, value: value})
	}
	return items
}

func requiredOrEmpty(required []any) []any {
	if required == nil {
		return []any{}
	}
	return required
}
