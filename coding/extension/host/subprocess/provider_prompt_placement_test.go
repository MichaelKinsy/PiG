package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

func TestPackedProviderResponseAndToolPrompts(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			configs := []ExtConfig{writePromptFactory(t, root, language, "prompt_one"), writePromptFactory(t, root, language, "prompt_two")}
			loaded, errs := host.LoadAll(t.Context(), configs)
			if len(errs) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", errs)
			}
			first, second := host.exts[configs[0].Name], host.exts[configs[1].Name]
			if first.packedCellKey == "" || first.packedCellKey != second.packedCellKey {
				t.Fatal("factories are not packed")
			}
			runner := inproc.NewRunner(loaded, t.TempDir())
			result, err := runner.Emit(t.Context(), extension.AfterProviderResponseEvent{Type: "after_provider_response", Status: 207, Headers: map[string]string{"x-probe": "received"}})
			if err != nil || result != nil {
				t.Fatalf("notification result = %#v, %v", result, err)
			}
			for _, tool := range runner.Tools() {
				result, err := tool.Definition.Execute(t.Context(), "probe", []byte(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				value, ok := result.(agent.AgentToolResult)
				if !ok || value.Text() != "after_provider_response:207:received" {
					t.Fatalf("response payload = %#v", result)
				}
			}
			options := prompts.WithToolDefinitions(prompts.Options{Cwd: "/probe", Tools: []string{"prompt_two", "prompt_one"}}, runner.Tools())
			prompt := prompts.BuildDefaultPrompt(options)
			if !strings.Contains(prompt, "- prompt_two: Tool summary\n- prompt_one: Tool summary") || !strings.Contains(prompt, "- prompt_two rule\n- shared\n- prompt_one rule") {
				t.Fatalf("prompt contributions:\n%s", prompt)
			}
			options.Tools = []string{"prompt_one"}
			prompt = prompts.BuildDefaultPrompt(options)
			if strings.Contains(prompt, "prompt_two") || !strings.Contains(prompt, "- prompt_one rule\n- shared") {
				t.Fatalf("inactive tool contribution:\n%s", prompt)
			}
		})
	}
}

func writePromptFactory(t *testing.T, root, language, name string) ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	switch language {
	case "go":
		module := "example.com/" + name
		write("go.mod", fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", module))
		write("extension.go", fmt.Sprintf(`package fixture
import ("fmt"; "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension {
 ext := sdk.New(%q)
 observed := "missing"
 ext.ToolWithGuidelines(%q,"not a snippet",sdk.Schema{"type":"object"},[]string{%q," shared ","shared"},func(sdk.Context,map[string]any)(any,error){return map[string]string{"content":observed},nil})
 ext.ToolPromptSnippet(%q," Tool\r\n summary ")
 ext.OnEvent("after_provider_response",func(_ sdk.Context,event map[string]any)(any,error){headers:=event["headers"].(map[string]any);observed=fmt.Sprintf("%%v:%%v:%%v",event["type"],event["status"],headers["x-probe"]);return map[string]any{"cancel":true},nil})
 return ext
}
`, name, name, name+" rule", name))
		return packedFactoryConfig(name, dir, module, name)
	case "python":
		write(name+".py", fmt.Sprintf(`import pig_sdk
def new_extension():
    ext = pig_sdk.Extension(%q)
    observed = "missing"
    ext.tool(%q, "not a snippet", {"type":"object"}, lambda ctx, args: {"content": observed}, prompt_snippet=" Tool\r\n summary ", prompt_guidelines=[%q," shared ","shared"])
    def response(ctx, event):
        nonlocal observed
        observed = "%%s:%%s:%%s" %% (event["type"], event["status"], event["headers"]["x-probe"])
        return {"cancel": True}
    ext.on_event("after_provider_response",response)
    return ext
`, name, name, name+" rule"))
		return packedPythonFactoryConfig(name, dir, name, name)
	case "rust":
		write("Cargo.toml", fmt.Sprintf("[package]\nname = %q\nversion = \"0.1.0\"\nedition = \"2024\"\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write("src/lib.rs", fmt.Sprintf(`use pig_sdk::{Extension,ToolResult};
use serde_json::json;
use std::sync::{Arc,Mutex};
pub fn new_extension() -> Extension {
 let mut ext = Extension::new(%q);
 let observed = Arc::new(Mutex::new("missing".to_string()));
 let tool_observed = observed.clone();
 ext.tool_with_guidelines(%q,"not a snippet",json!({"type":"object"}),vec![%q.into()," shared ".into(),"shared".into()],move |_,_| ToolResult::json(json!({"content":*tool_observed.lock().unwrap()})));
 ext.tool_prompt_snippet(%q," Tool\r\n summary ");
 ext.on_event("after_provider_response",false,move |_,event|{*observed.lock().unwrap()=format!("{}:{}:{}",event["type"].as_str().unwrap(),event["status"],event["headers"]["x-probe"].as_str().unwrap());Some(json!({"cancel":true}))});
 ext
}
`, name, name, name+" rule", name))
		return packedRustFactoryConfig(name, dir, name, name)
	default:
		t.Fatalf("unknown language %s", language)
		return ExtConfig{}
	}
}
