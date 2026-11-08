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
