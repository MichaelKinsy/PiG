package harness

// pi: packages/durable/src/harness/agent.ts (resolveAgent, selectExtensions, applyWrap)

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type agentWrapSpec struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
}

type agentExtensionSpec struct {
	Name     string              `json:"name"`
	Tools    []map[string]string `json:"tools"`
	Sections []map[string]string `json:"sections"`
	Wraps    []agentWrapSpec     `json:"wraps"`
}

type agentCase struct {
	Extensions []agentExtensionSpec `json:"extensions"`
	Settings   []string             `json:"settings,omitzero"`
	State      json.RawMessage      `json:"state,omitempty"`
}

type agentSnapshot struct{ extensions []*durable.Extension }

func (s agentSnapshot) Installed() []*durable.Extension { return s.extensions }
func (s agentSnapshot) Extension(name string) *durable.Extension {
	for _, extension := range s.extensions {
		if extension.Name == name {
			return extension
		}
	}
	return nil
}
func (agentSnapshot) Tools() []durable.RegistryTool       { return nil }
func (agentSnapshot) Sections() []durable.RegistrySection { return nil }
func (agentSnapshot) Tasks() []durable.AnyTask            { return nil }
func (agentSnapshot) Task(string) durable.AnyTask         { return durable.AnyTask{} }

func agentCases() []agentCase {
	random := rand.New(rand.NewSource(20261027))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	toolNames := []string{"t1", "t2", "t3", "t4", "t5"}
	sectionKeys := []string{"s1", "s2", "s3", "s4", "instructions"}
	extensionNames := []string{"e1", "e2", "e3", "e4"}
	subset := func(from []string, max int) []string {
		list := []string{}
		for range random.Intn(max + 1) {
			list = append(list, from[random.Intn(len(from))])
		}
		return list
	}
	cases := make([]agentCase, 0, 3000)
	for range 3000 {
		spec := agentCase{}
		for _, name := range extensionNames[:1+random.Intn(len(extensionNames))] {
			extension := agentExtensionSpec{Name: name, Tools: []map[string]string{}, Sections: []map[string]string{}, Wraps: []agentWrapSpec{}}
			for _, tool := range subset(toolNames, 3) {
				extension.Tools = append(extension.Tools, map[string]string{"name": tool, "description": name + "/" + tool})
			}
			for _, key := range subset(sectionKeys, 3) {
				extension.Sections = append(extension.Sections, map[string]string{"key": key})
			}
			for range random.Intn(4) {
				if random.Intn(2) == 0 {
					extension.Wraps = append(extension.Wraps, agentWrapSpec{Kind: "tool", Target: pick(toolNames...), Mode: pick("tag", "tag", "rename", "throw")})
				} else {
					extension.Wraps = append(extension.Wraps, agentWrapSpec{Kind: "section", Target: pick(sectionKeys...), Mode: pick("tag", "tag", "rename", "throw")})
				}
			}
			spec.Extensions = append(spec.Extensions, extension)
		}
		if random.Intn(3) == 0 {
			spec.Settings = subset(extensionNames, 4)
		}
		if random.Intn(5) != 0 {
			state := map[string]any{}
			switch random.Intn(4) {
			case 0:
				state["extensions"] = subset(extensionNames, 5)
			case 1:
				edit := map[string]any{}
				if random.Intn(2) == 0 {
					edit["add"] = subset(extensionNames, 3)
				}
				if random.Intn(2) == 0 {
					edit["remove"] = subset(extensionNames, 3)
				}
				state["extensions"] = edit
			}
			switch random.Intn(4) {
			case 0:
				state["tools"] = subset(toolNames, 6)
			case 1:
				state["tools"] = map[string]any{"remove": subset(toolNames, 3)}
			}
			if random.Intn(3) == 0 {
				state["instructions"] = pick("", "be brief", "multi\nline")
			}
			if random.Intn(3) == 0 {
				state["cwd"] = pick("/", "/work")
			}
			if random.Intn(3) == 0 {
				state["thinkingLevel"] = pick("low", "high", "off")
			}
			if random.Intn(3) == 0 {
				state["model"] = map[string]any{"provider": "p", "modelId": "m"}
			}
			spec.State, _ = json.Marshal(state)
		}
		cases = append(cases, spec)
	}
	return cases
}

func TestResolveAgentMatchesPi(t *testing.T) {
	cases := agentCases()
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/agent_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	differing, wrapped, reported := 0, 0, 0
	for i, spec := range cases {
		counter := 0
		extensions := make([]*durable.Extension, len(spec.Extensions))
		for e, source := range spec.Extensions {
			extension := &durable.Extension{Name: source.Name}
			for _, tool := range source.Tools {
				extension.Tools = append(extension.Tools, &durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: tool["name"], Description: tool["description"]}})
			}
			for _, section := range source.Sections {
				key := section["key"]
				text := "R:" + key + ":" + source.Name
				extension.Sections = append(extension.Sections, &durable.PromptSection{Key: key, Render: func(context.Context, durable.PromptInput) (*string, error) { return &text, nil }})
			}
			for _, wrap := range source.Wraps {
				mode := wrap.Mode
				if wrap.Kind == "tool" {
					extension.Wraps = append(extension.Wraps, durable.ToolWrap{Tool: wrap.Target, Wrap: func(tool *durable.ToolRegistration) *durable.ToolRegistration {
						switch mode {
						case "throw":
							panic(errors.New("boom"))
						case "rename":
							copied := *tool
							copied.Name += "x"
							return &copied
						}
						counter++
						copied := *tool
						copied.Description += "+" + strconv.Itoa(counter)
						return &copied
					}})
				} else {
					extension.Wraps = append(extension.Wraps, durable.SectionWrap{Section: wrap.Target, Wrap: func(section *durable.PromptSection) *durable.PromptSection {
						switch mode {
						case "throw":
							panic(errors.New("boom"))
						case "rename":
							copied := *section
							copied.Key += "x"
							return &copied
						}
						copied := *section
						inner := section.Render
						copied.Render = func(ctx context.Context, input durable.PromptInput) (*string, error) {
							text, err := inner(ctx, input)
							if err != nil || text == nil {
								return text, err
							}
							counter++
							return new(*text + "+" + strconv.Itoa(counter)), nil
						}
						return &copied
					}})
				}
				wrapped++
			}
			extensions[e] = extension
		}
		settings := durable.Settings{}
		if spec.Settings != nil {
			settings.Extensions = []*durable.Extension{}
			for _, name := range spec.Settings {
				settings.Extensions = append(settings.Extensions, &durable.Extension{Name: name})
			}
		}
		var state *AgentState
		if spec.State != nil {
			state = &AgentState{}
			if err := json.Unmarshal(spec.State, state); err != nil {
				t.Fatalf("%s: %v", spec.State, err)
			}
		}
		reports := []string{}
		agent := ResolveAgent(state, agentSnapshot{extensions}, settings, func(err error, _ string) { reports = append(reports, err.Error()) })
		reported += len(reports)
		extensionList := []string{}
		for _, extension := range agent.Extensions {
			extensionList = append(extensionList, extension.Name)
		}
		tools := []map[string]string{}
		for _, tool := range agent.Tools {
			tools = append(tools, map[string]string{"name": tool.Name, "description": tool.Description})
		}
		sections := []map[string]string{}
		for _, section := range agent.Sections {
			text, err := section.Render(context.Background(), durable.PromptInput{})
			if err != nil || text == nil {
				t.Fatalf("section %s: %v", section.Key, err)
			}
			sections = append(sections, map[string]string{"key": section.Key, "text": *text})
		}
		got := map[string]any{"thinkingLevel": string(agent.ThinkingLevel), "extensions": extensionList, "tools": tools, "sections": sections}
		if agent.Model != nil {
			got["model"] = agent.Model
		}
		if agent.Instructions != nil {
			got["instructions"] = *agent.Instructions
		}
		if agent.Cwd != nil {
			got["cwd"] = *agent.Cwd
		}
		var want struct {
			Agent   any      `json:"agent"`
			Reports []string `json:"reports"`
		}
		if err := json.Unmarshal(expected[i], &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(canonical(got), want.Agent) || !reflect.DeepEqual(reports, want.Reports) {
			differing++
			if differing <= 5 {
				gotJSON, _ := json.Marshal(got)
				caseJSON, _ := json.Marshal(spec)
				t.Errorf("case %d\n  case %.1500s\n  Pi  %.900s\n  Pig %.900s reports %v", i, caseJSON, expected[i], gotJSON, reports)
			}
		}
	}
	t.Logf("%d cases, %d wraps, %d reports, %d differing", len(cases), wrapped, reported, differing)
	if differing > 0 {
		t.Errorf("%d cases differ from Pi", differing)
	}
}
