package mcpext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
)

func TestChannelMetaFilteringAndEnvelope(t *testing.T) {
	message, ok := channelMessage(`a"'&`, json.RawMessage(`{"content":"<hello>&</channel>","meta":{"event_id":"x","ABC_012":"\"'<&","bad-key":"no","":"no","é":"no","source":"spoof","number":1,"nil":null,"object":{},"array":[],"bool":true}}`))
	if !ok {
		t.Fatal("message rejected")
	}
	want := `<channel source="a&#34;&#39;&amp;" ABC_012="&#34;&#39;&lt;&amp;" event_id="x">&lt;hello&gt;&amp;&lt;/channel&gt;</channel>`
	if message.Content != want {
		t.Fatalf("content=%s want=%s", message.Content, want)
	}
	if message.CustomType != "mcp-channel" || !message.Display {
		t.Fatalf("message=%+v", message)
	}
	details := message.Details.(map[string]any)
	if details["source"] != `a"'&` || len(details["meta"].(map[string]string)) != 2 {
		t.Fatalf("details=%+v", details)
	}
}

func TestChannelContentPreservesQuotesWithoutClosingWrapper(t *testing.T) {
	message, ok := channelMessage("server", json.RawMessage(`{"content":"Say \"don't\" & </channel>"}`))
	want := `<channel source="server">Say "don't" &amp; &lt;/channel&gt;</channel>`
	if !ok || message.Content != want {
		t.Fatalf("content = %q, accepted = %v; want %q", message.Content, ok, want)
	}
}

func TestChannelPayloadValidation(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `[]`, `{"content":null}`, `{"content":7}`, `{"content":"ok","meta":[]}`, `{"content":"ok","meta":"bad"}`, `{"content":"` + strings.Repeat("x", ChannelMaxBytes) + `"}`} {
		if _, ok := channelMessage("server", json.RawMessage(raw)); ok {
			t.Fatalf("accepted invalid payload: %.80s", raw)
		}
	}
	if _, ok := channelMessage("server", json.RawMessage(`{"content":"","meta":null}`)); !ok {
		t.Fatal("empty content rejected")
	}
}

func TestChannelConfigOptIn(t *testing.T) {
	for _, value := range []string{`true`, `false`, `null`, `"true"`, `123`} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{"mcpServers":{"push":{"command":"unused","channel":`+value+`},"normal":{"command":"unused"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			config := LoadMcpConfig(LoadOptions{AgentDir: dir})
			if value == `"true"` || value == `123` {
				if len(config.Errors) != 1 || len(config.Servers) != 1 || config.Servers[0].Name != "normal" {
					t.Fatalf("config=%+v", config)
				}
				return
			}
			if len(config.Errors) != 0 || len(config.Servers) != 2 || config.Servers[0].Channel != (value == "true") || config.Servers[1].Channel {
				t.Fatalf("config=%+v", config)
			}
		})
	}
}

func TestChannelAdapterUsesCustomSteering(t *testing.T) {
	fake := extensiontest.NewFake()
	message, _ := channelMessage("server", json.RawMessage(`{"content":"wake"}`))
	apiHost{newPiAdapter(fake)}.SendChannel(message)
	if len(fake.SendMessageCalls) != 1 || len(fake.SendUserMessageCalls) != 0 {
		t.Fatal("channel lost custom provenance")
	}
	call := fake.SendMessageCalls[0]
	if call.Message.CustomType != "mcp-channel" || call.Options.DeliverAs != extension.DeliverAsSteer || call.Options.TriggerTurn == nil || !*call.Options.TriggerTurn {
		t.Fatalf("call=%+v", call)
	}
}
