package sdk

import (
	"bytes"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SystemPromptSection is one authored prompt section before XML wrapping.
type SystemPromptSection struct {
	Name  string
	Value string
}

// SystemPromptSections preserves the insertion order of Pi's section object. before_agent_start exposes a pointer to this collection at data["systemPromptOptions"].(map[string]any)["sections"].
type SystemPromptSections []SystemPromptSection

// Set replaces an existing section in place or appends a new section.
func (s *SystemPromptSections) Set(name, value string) {
	if index := slices.IndexFunc(*s, func(section SystemPromptSection) bool { return section.Name == name }); index >= 0 {
		(*s)[index].Value = value
	} else {
		*s = append(*s, SystemPromptSection{Name: name, Value: value})
	}
}

// Delete removes a section without reordering the remaining sections.
func (s *SystemPromptSections) Delete(name string) {
	*s = slices.DeleteFunc(*s, func(section SystemPromptSection) bool { return section.Name == name })
}

func (s SystemPromptSections) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for i, section := range s {
		if i > 0 {
			out = append(out, ',')
		}
		name, err := json.MarshalJS(section.Name)
		if err != nil {
			return nil, err
		}
		value, err := json.MarshalJS(section.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, name...)
		out = append(out, ':')
		out = append(out, value...)
	}
	return append(out, '}'), nil
}

func (s *SystemPromptSections) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("system prompt sections must be an object")
	}
	sections := SystemPromptSections{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return err
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		sections.Set(name.(string), value)
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*s = sections
	return nil
}

// preparePromptOptions gives a before_agent_start handler Pi's normalized options: an ordered section collection and every other collection, empty when the host omitted it. The handler may edit or replace any field; the response returns them all.
func preparePromptOptions(raw json.RawMessage, data map[string]any) (map[string]any, error) {
	var event struct {
		Options struct {
			Sections SystemPromptSections `json:"sections"`
		} `json:"systemPromptOptions"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	options, _ := data["systemPromptOptions"].(map[string]any)
	if options == nil {
		return nil, fmt.Errorf("before_agent_start requires systemPromptOptions")
	}
	sections := &event.Options.Sections
	options["sections"] = sections
	for name, empty := range map[string]func() any{
		"selectedTools":    func() any { return []any{} },
		"hiddenTools":      func() any { return []any{} },
		"promptGuidelines": func() any { return []any{} },
		"contextFiles":     func() any { return []any{} },
		"skills":           func() any { return []any{} },
		"toolSnippets":     func() any { return map[string]any{} },
		"toolGuidelines":   func() any { return map[string]any{} },
	} {
		if _, ok := options[name]; !ok {
			options[name] = empty()
		}
	}
	return options, nil
}

// SystemPrompt is before_agent_start's data["systemPrompt"]: Pi's event.systemPrompt getter (runner.ts:1430-1449), which
// renders the emission's shared options when read, so a handler sees its own edit to data["systemPromptOptions"] at once.
// Context.GetSystemPrompt returns the same value in that handler.
type SystemPrompt struct {
	ctx     Context
	options map[string]any
	mu      sync.Mutex
	key     []byte
	prompt  string
}

// newSystemPrompt holds the prompt the host rendered for the options the handler received; options that differ from those
// are rendered again by the host.
func newSystemPrompt(ctx Context, options map[string]any, rendered any) (*SystemPrompt, error) {
	key, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	prompt, _ := rendered.(string)
	return &SystemPrompt{ctx: ctx, options: options, key: key, prompt: prompt}, nil
}

// Get returns the prompt the shared options render now. An invalid custom section is an error, as Pi's builder throws.
func (p *SystemPrompt) Get() (string, error) {
	key, err := json.Marshal(p.options)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !bytes.Equal(key, p.key) {
		prompt, err := hostRequired[string](p.ctx, "renderSystemPrompt", map[string]json.RawMessage{"options": key}, "prompt")
		if err != nil {
			return "", err
		}
		p.key, p.prompt = key, prompt
	}
	return p.prompt, nil
}

// MarshalJSON encodes the prompt the options render now, as JSON.stringify reads Pi's getter.
func (p *SystemPrompt) MarshalJSON() ([]byte, error) {
	prompt, err := p.Get()
	if err != nil {
		return nil, err
	}
	return json.Marshal(prompt)
}
