package mcpext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/mcp"
)

type oracleResourceServer struct {
	spec oracleServerSpec
}

type oracleServerSpec struct {
	Name      string            `json:"name"`
	Resources []json.RawMessage `json:"resources"`
	Templates []json.RawMessage `json:"templates"`
	Next      *string           `json:"next"`
	Contents  []json.RawMessage `json:"contents"`
	Fail      string            `json:"fail"`
}

func (s oracleResourceServer) Name() string           { return s.spec.Name }
func (s oracleResourceServer) Timeout() time.Duration { return time.Second }
func (s oracleResourceServer) failure() error {
	switch s.spec.Fail {
	case "error":
		return errors.New("boom")
	case "string":
		return errors.New("plain string rejection")
	}
	return nil
}
func (s oracleResourceServer) next() string {
	if s.spec.Next == nil {
		return ""
	}
	return *s.spec.Next
}
func (s oracleResourceServer) resources() []mcp.Resource {
	out := make([]mcp.Resource, len(s.spec.Resources))
	for i, raw := range s.spec.Resources {
		_ = json.Unmarshal(raw, &out[i])
		out[i].Raw = raw
	}
	return out
}
func (s oracleResourceServer) templates() []mcp.ResourceTemplate {
	out := make([]mcp.ResourceTemplate, len(s.spec.Templates))
	for i, raw := range s.spec.Templates {
		_ = json.Unmarshal(raw, &out[i])
		out[i].Raw = raw
	}
	return out
}
func (s oracleResourceServer) ResourcesPage(context.Context, *string, mcp.RequestOptions) (*mcp.ListResourcesResult, error) {
	if err := s.failure(); err != nil {
		return nil, err
	}
	return &mcp.ListResourcesResult{Resources: s.resources(), NextCursor: s.next(), HasNextCursor: s.spec.Next != nil}, nil
}
func (s oracleResourceServer) ResourceTemplatesPage(context.Context, *string, mcp.RequestOptions) (*mcp.ListResourceTemplatesResult, error) {
	if err := s.failure(); err != nil {
		return nil, err
	}
	return &mcp.ListResourceTemplatesResult{ResourceTemplates: s.templates(), NextCursor: s.next(), HasNextCursor: s.spec.Next != nil}, nil
}
func (s oracleResourceServer) AllResources(context.Context, mcp.RequestOptions) ([]mcp.Resource, error) {
	return s.resources(), s.failure()
}
func (s oracleResourceServer) AllResourceTemplates(context.Context, mcp.RequestOptions) ([]mcp.ResourceTemplate, error) {
	return s.templates(), s.failure()
}
func (s oracleResourceServer) ReadResource(context.Context, string, mcp.RequestOptions) (*mcp.ReadResourceResult, error) {
	if err := s.failure(); err != nil {
		return nil, err
	}
	contents := make([]mcp.ResourceContents, len(s.spec.Contents))
	for i, raw := range s.spec.Contents {
		_ = json.Unmarshal(raw, &contents[i])
		contents[i].Raw = raw
	}
	return &mcp.ReadResourceResult{Contents: contents}, nil
}

// The three resource tools (mcp/resources.js) against Pi: server and cursor arguments of every type (padded, blank, null, numbers, objects), unknown servers, the cursor-without-server rule, the localeCompare server order, per-server
// listing errors, MCP App resources and icons left out, members kept in the server's order, one page with its next cursor, and read results with zero, one and several contents.
func TestMcpResourceToolsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	names := []string{"a-b", "ab", "a_b", "Ab", "é", "e", "f", "alpha", "Beta", "gamma", "日本", "a b", "résumé", "Zed", "10", "9", "srv"}
	resource := func(template bool) json.RawMessage {
		members := []string{}
		if template {
			members = append(members, fmt.Sprintf(`"uriTemplate":%q`, pick("file:///{path}", "ui://app/{id}", "https://x.test/{a}")))
		} else {
			members = append(members, fmt.Sprintf(`"uri":%q`, pick("file:///a.txt", "ui://app", "https://x.test/b", "file:///日本")))
		}
		members = append(members, fmt.Sprintf(`"name":%q`, pick("n", "name two", "日本")))
		if rng.Intn(2) == 0 {
			members = append(members, `"title":"T"`)
		}
		if rng.Intn(3) == 0 {
			members = append(members, fmt.Sprintf(`"mimeType":%q`, pick("text/plain", "text/html;profile=mcp-app", `text/html ; profile = "MCP-App"`, "application/json")))
		}
		if rng.Intn(3) == 0 {
			members = append(members, `"_meta":{"k":1}`)
		}
		if rng.Intn(3) == 0 {
			members = append(members, `"icons":[{"src":"x"}]`)
		}
		if rng.Intn(4) == 0 {
			members = append(members, `"size":`+pick("1", "1.5", "1e21", "0"))
		}
		if rng.Intn(5) == 0 {
			members = append(members, `"extra":{"z":1,"a":[1,2],"10":1,"2":2}`)
		}
		rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		return json.RawMessage("{" + strings.Join(members, ",") + "}")
	}
	content := func() json.RawMessage {
		switch rng.Intn(3) {
		case 0:
			return json.RawMessage(fmt.Sprintf(`{"uri":%q,"mimeType":"text/plain","text":%q,"_meta":{"a":1}}`, pick("file:///a", "file:///b"), pick("hello", "", "multi\nline", "日本")))
		case 1:
			return json.RawMessage(fmt.Sprintf(`{"uri":%q,"text":%q}`, pick("file:///a", "dir/"), pick("x", "y")))
		}
		return json.RawMessage(`{"uri":"file:///i.png","mimeType":"image/png","blob":"iVBORw0KGgo="}`)
	}
	type probe struct {
		Tool    string             `json:"tool"`
		Params  *string            `json:"params"`
		Servers []oracleServerSpec `json:"servers"`
	}
	var probes []probe
	for range 3000 {
		p := probe{Tool: pick(ListMcpResourcesTool, ListMcpResourceTemplatesTool, "read_mcp_resource")}
		used := map[string]bool{}
		for range 1 + rng.Intn(4) {
			name := pick(names...)
			if used[name] {
				continue
			}
			used[name] = true
			spec := oracleServerSpec{Name: name, Resources: []json.RawMessage{}, Templates: []json.RawMessage{}, Contents: []json.RawMessage{}}
			for range rng.Intn(4) {
				spec.Resources = append(spec.Resources, resource(false))
				spec.Templates = append(spec.Templates, resource(true))
			}
			for range rng.Intn(4) {
				spec.Contents = append(spec.Contents, content())
			}
			if rng.Intn(3) == 0 {
				next := pick("cursor-1", "日本", "")
				spec.Next = &next
			}
			if rng.Intn(6) == 0 {
				spec.Fail = pick("error", "string")
			}
			p.Servers = append(p.Servers, spec)
		}
		var server, cursor, uri string
		serverValue := func() string {
			return pick(`"alpha"`, `" alpha "`, `"Beta"`, `"nope"`, `""`, `"   "`, `null`, `5`, `{}`, `true`, `"srv"`, `"\u00a0srv\u00a0"`, `"\u0085srv"`, fmt.Sprintf("%q", pick(names...)))
		}
		server, cursor, uri = serverValue(), pick(`"c1"`, `" c1 "`, `""`, `null`, `7`, `"  "`), pick(`"file:///a"`, `" file:///a "`, `""`, `null`, `3`, `"file:///b"`)
		members := []string{}
		if rng.Intn(5) != 0 {
			members = append(members, `"server":`+server)
		}
		if rng.Intn(2) == 0 {
			members = append(members, `"cursor":`+cursor)
		}
		if rng.Intn(2) == 0 || p.Tool == "read_mcp_resource" {
			members = append(members, `"uri":`+uri)
		}
		params := "{" + strings.Join(members, ",") + "}"
		switch rng.Intn(12) {
		case 0:
			p.Params = nil
		case 1:
			params = pick(`null`, `[]`, `"text"`, `5`)
			p.Params = &params
		default:
			p.Params = &params
		}
		probes = append(probes, p)
	}
	type outcome struct {
		Content    []map[string]any `json:"content"`
		Details    map[string]any   `json:"details"`
		Structured string           `json:"structured"`
		Error      string           `json:"error"`
	}
	var want []outcome
	pioracle.Run(t, `
const { createMcpResourceToolDefinitions } = await load("pi-coding-agent/extensions/mcp/resources.js");
const out = [];
for (const p of input) {
	const servers = (p.servers ?? []).map((s) => {
		const fail = () => { if (s.fail === "error") throw new Error("boom"); if (s.fail === "string") throw "plain string rejection"; };
		return {
			name: s.name,
			timeoutMs: 1000,
			async resourcesPage() { fail(); return { resources: s.resources, ...(s.next === null ? {} : { nextCursor: s.next }) }; },
			async resourceTemplatesPage() { fail(); return { resourceTemplates: s.templates, ...(s.next === null ? {} : { nextCursor: s.next }) }; },
			async allResources() { fail(); return s.resources; },
			async allResourceTemplates() { fail(); return s.templates; },
			async readResource() { fail(); return { contents: s.contents }; },
		};
	});
	const tools = createMcpResourceToolDefinitions({ exposure: "codemode", servers: () => servers });
	const tool = tools.find((t) => t.name === p.tool);
	const params = p.params === null ? undefined : JSON.parse(p.params);
	try {
		const r = await tool.execute("call", params, undefined);
		out.push({ content: r.content, details: r.details, structured: JSON.stringify(r.structuredContent), error: "" });
	} catch (e) { out.push({ content: [], details: {}, structured: "", error: e instanceof Error ? e.message : String(e) }); }
}
emit(out);
`, probes, &want)
	bad := 0
	for i, p := range probes {
		servers := make([]McpResourceServer, len(p.Servers))
		for j, spec := range p.Servers {
			servers[j] = oracleResourceServer{spec}
		}
		var params json.RawMessage
		if p.Params != nil {
			params = json.RawMessage(*p.Params)
		}
		var definition extension.ToolDefinition
		for _, d := range CreateMcpResourceToolDefinitions(ResourceToolsOptions{Exposure: extension.McpExposureCodemode, Servers: func() []McpResourceServer { return servers }}) {
			if d.Name == p.Tool {
				definition = d
			}
		}
		got, err := definition.Execute(t.Context(), "call", params, nil)
		w := want[i]
		var problem string
		switch {
		case err != nil && w.Error == "":
			problem = "Go error " + err.Error() + ", Pi succeeded"
		case err != nil && err.Error() != w.Error:
			problem = fmt.Sprintf("error %q, Pi %q", err.Error(), w.Error)
		case err == nil && w.Error != "":
			problem = "Go succeeded, Pi error " + w.Error
		case err == nil:
			content, _ := json.Marshal(got.Content)
			details, _ := json.Marshal(got.Details)
			wantContent, _ := json.Marshal(w.Content)
			wantDetails, _ := json.Marshal(w.Details)
			var a, b any
			_ = json.Unmarshal(content, &a)
			_ = json.Unmarshal(wantContent, &b)
			var c, d any
			_ = json.Unmarshal(details, &c)
			_ = json.Unmarshal(wantDetails, &d)
			if fmt.Sprint(a) != fmt.Sprint(b) || fmt.Sprint(c) != fmt.Sprint(d) || string(got.StructuredContent) != w.Structured {
				problem = fmt.Sprintf("\n got content=%s details=%s structured=%s\nwant content=%s details=%s structured=%s", content, details, got.StructuredContent, wantContent, wantDetails, w.Structured)
			}
		}
		if problem != "" {
			if bad++; bad <= 6 {
				params := "<undefined>"
				if p.Params != nil {
					params = *p.Params
				}
				t.Errorf("probe %d %s params=%s servers=%d: %s", i, p.Tool, params, len(p.Servers), problem)
			}
		}
	}
	if bad > 6 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
