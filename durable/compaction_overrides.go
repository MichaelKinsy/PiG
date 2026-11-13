package durable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// CompactionModelOverride is one entry of modelOverrides: the exact "provider/modelId" and the fields it sets.
type CompactionModelOverride struct {
	Model    string
	Override CompactionPolicyOverride
}

// CompactionOverrides is CompactionSettings.modelOverrides: an object keyed by exact "provider/modelId" whose entries keep the order they
// were set in, as a JavaScript object does (Pi stores and returns them in that order). The zero value is the absent modelOverrides; an
// empty set made by NewCompactionOverrides or decoded from {} is an explicit {}.
type CompactionOverrides struct{ entries []CompactionModelOverride }

// NewCompactionOverrides returns the overrides of entries in order. A model named twice keeps its first position and takes the last
// override, as assigning an existing key of a JavaScript object does.
func NewCompactionOverrides(entries ...CompactionModelOverride) CompactionOverrides {
	set := CompactionOverrides{entries: make([]CompactionModelOverride, 0, len(entries))}
	for _, entry := range entries {
		set.Set(entry.Model, entry.Override)
	}
	return set
}

// Set stores override at model: an existing model keeps its position, a new one is appended.
func (set *CompactionOverrides) Set(model string, override CompactionPolicyOverride) {
	if i := set.index(model); i >= 0 {
		set.entries[i].Override = override
		return
	}
	set.entries = append(set.entries, CompactionModelOverride{Model: model, Override: override})
}

// Get returns the override of model and whether the object has that key, as indexing a JavaScript object does; the zero override,
// which sets no field, when it has none.
func (set CompactionOverrides) Get(model string) (CompactionPolicyOverride, bool) {
	if i := set.index(model); i >= 0 {
		return set.entries[i].Override, true
	}
	return CompactionPolicyOverride{}, false
}

// IsSet reports whether modelOverrides is present, even when empty.
func (set CompactionOverrides) IsSet() bool { return set.entries != nil }

// Len is the number of models.
func (set CompactionOverrides) Len() int { return len(set.entries) }

// All iterates the entries in insertion order.
func (set CompactionOverrides) All() iter.Seq2[string, CompactionPolicyOverride] {
	return func(yield func(string, CompactionPolicyOverride) bool) {
		for _, entry := range set.entries {
			if !yield(entry.Model, entry.Override) {
				return
			}
		}
	}
}

func (set CompactionOverrides) index(model string) int {
	return slices.IndexFunc(set.entries, func(entry CompactionModelOverride) bool { return entry.Model == model })
}

// MarshalJSON writes the entries as an object in insertion order.
func (set CompactionOverrides) MarshalJSON() ([]byte, error) {
	if set.entries == nil {
		return []byte("null"), nil
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, entry := range set.entries {
		if i > 0 {
			out.WriteByte(',')
		}
		//perf:ok cold: compaction settings are encoded when a conversation is configured, not per turn
		key, err := jsstring.MarshalKeepingSurrogates(entry.Model)
		if err != nil {
			return nil, err
		}
		//perf:ok cold: compaction settings are encoded when a conversation is configured, not per turn
		value, err := jsstring.MarshalKeepingSurrogates(entry.Override)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// UnmarshalJSON reads an object, keeping the order of its members; a repeated member takes the last value in its first position.
func (set *CompactionOverrides) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if first == nil {
		set.entries = nil
		return nil
	}
	if first != json.Delim('{') {
		return fmt.Errorf("modelOverrides must be an object, got %v", first)
	}
	decoded := CompactionOverrides{entries: []CompactionModelOverride{}}
	for decoder.More() {
		start := decoder.InputOffset()
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		if _, ok := key.(string); !ok {
			return errors.New("modelOverrides has a key that is not a string")
		}
		// Token reads a lone-surrogate escape as U+FFFD; the key's own text is read again as JSON.parse reads it.
		var model string
		if err := jsstring.UnmarshalKeepingSurrogates(quotedAfter(data, start, decoder.InputOffset()), &model); err != nil {
			return err
		}
		var override CompactionPolicyOverride
		if err := decoder.Decode(&override); err != nil {
			return fmt.Errorf("modelOverrides[%q]: %w", model, err)
		}
		decoded.Set(model, override)
	}
	*set = decoded
	return nil
}

// quotedAfter is the string literal that ends at end, the first one at or after start: the text of the key the decoder just read.
func quotedAfter(data []byte, start, end int64) []byte {
	open := bytes.IndexByte(data[start:end], '"')
	return data[start+int64(open) : end]
}
