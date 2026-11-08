package runtime

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// apps/p2/src/runtime/entries.ts and input.ts have no unit test upstream: inputs.test.ts and sharing.test.ts read these
// data through the transcript and the worker (lanes B and E). These cases pin the open-object schemas.

func decode(t *testing.T, text string) any {
	t.Helper()
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestIsMessageData(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{`{"author":{"kind":"owner","id":"owner","name":"Mario"},"text":"hi","attachments":[]}`, true},
		{`{"author":{"kind":"guest","id":"g","name":"Eve","extra":1},"text":"","attachments":[{"id":"u","name":"a","x":2}],"more":true}`, true},
		{`{"author":{"kind":"robot","id":"g","name":"Eve"},"text":"hi","attachments":[]}`, false},
		{`{"author":{"kind":"owner","id":"owner","name":"Mario"},"text":"hi"}`, false},
		{`{"text":"hi","attachments":[]}`, false},
	} {
		text, want := c.text, c.want
		if got := IsMessageData(decode(t, text)); got != want {
			t.Errorf("IsMessageData(%s) = %v, want %v", text, got, want)
		}
	}
	if IsMessageData(nil) || IsMessageData("x") {
		t.Error("a non-object is message data")
	}
}

func TestPeerAndAgentMessages(t *testing.T) {
	peer := decode(t, `{"from":{"id":"s-1","title":"Alpha"},"text":"hi from alpha","origin":"owner"}`)
	if !IsPeerMessage(PeerMessageEntry, peer) || IsPeerMessage("p2.input", peer) {
		t.Error("peer message kind")
	}
	if IsPeerMessage(PeerMessageEntry, decode(t, `{"from":{"id":"s-1","title":"Alpha"},"text":"x","origin":"guest"}`)) {
		t.Error("origin other than owner accepted")
	}
	agent := decode(t, `{"from":{"name":"worker","conversationId":2},"intent":"answered","text":"done"}`)
	if !IsAgentMessage(AgentMessageEntry, agent) || IsAgentMessage(PeerMessageEntry, agent) {
		t.Error("agent message kind")
	}
	if IsAgentMessage(AgentMessageEntry, decode(t, `{"from":{"name":"w","conversationId":2},"intent":"shrug","text":"x"}`)) {
		t.Error("unknown intent accepted")
	}
}

func TestTypedReaders(t *testing.T) {
	peer, ok := PeerMessageOf(PeerMessageEntry, decode(t, `{"from":{"id":"s-1","title":"Alpha"},"text":"hi","origin":"owner","extra":1}`))
	if !ok || peer.FromID != "s-1" || peer.FromTitle != "Alpha" || peer.Text != "hi" || peer.Origin == nil || *peer.Origin != OriginOwner {
		t.Errorf("peer = %+v, %v", peer, ok)
	}
	if _, ok := PeerMessageOf("p2.input", decode(t, `{}`)); ok {
		t.Error("a peer message of the wrong kind")
	}
	agent, ok := AgentMessageOf(AgentMessageEntry, decode(t, `{"from":{"name":"w","conversationId":2},"intent":"question","text":"?"}`))
	if !ok || agent.FromName != "w" || agent.FromConversationID != 2 || agent.Intent != IntentQuestion || agent.Origin != nil {
		t.Errorf("agent = %+v, %v", agent, ok)
	}
	data, ok := MessageDataOf(decode(t, `{"author":{"kind":"guest","id":"g","name":"Eve"},"text":"hi","attachments":[{"id":"u","name":"a.txt"}]}`))
	if !ok || data.Author != (Author{Kind: AuthorGuest, ID: "g", Name: "Eve"}) || data.Text != "hi" || len(data.Attachments) != 1 || data.Attachments[0] != (MessageAttachment{ID: "u", Name: "a.txt"}) {
		t.Errorf("message data = %+v, %v", data, ok)
	}
	if _, ok := MessageDataOf(decode(t, `{"text":"hi"}`)); ok {
		t.Error("message data without an author")
	}
	info, ok := ArtifactInfoOf(decode(t, `{"name":"plan-1","title":"Plan","type":"markdown","version":2}`))
	if !ok || info != (ArtifactInfo{Name: "plan-1", Title: "Plan", Type: ArtifactMarkdown, Version: 2}) {
		t.Errorf("artifact = %+v, %v", info, ok)
	}
	for _, bad := range []string{`{"name":"Plan","title":"x","type":"html","version":1}`, `{"name":"p","title":"x","type":"pdf","version":1}`, `{"name":"p","title":"x","type":"svg","version":0}`} {
		if _, ok := ArtifactInfoOf(decode(t, bad)); ok {
			t.Errorf("accepted %s", bad)
		}
	}
}
