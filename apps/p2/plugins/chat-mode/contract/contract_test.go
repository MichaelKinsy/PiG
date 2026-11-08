package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contract fingerprints equal Pi's (see sessions/contract).
func TestChatModeContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/chat-mode/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, contract := range map[string]*contracts.Contract{"ChatModeSetting": ChatModeSetting, "ChatMode": ChatMode} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("%s: Pi exports no such contract", name)
			continue
		}
		if contract.Name != want.Name || contract.Version != want.Version || contract.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, contract.Name, contract.Version, contract.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}

// The mention rules of chat-mode.test.ts "chat-mode mentions", on the default name and a renamed agent.
func TestMentionsAgentAsAWordOfItsOwnInAnyCase(t *testing.T) {
	for _, text := range []string{"@pi", "hey @Pi, look", "(@PI)", "ok @pi."} {
		if !MentionsAgent(text, DefaultAgentName) {
			t.Errorf("%q does not mention @pi", text)
		}
	}
	for _, text := range []string{"@pizza", "me@pi", "pi", "@@pi", "@pi_bot"} {
		if MentionsAgent(text, DefaultAgentName) {
			t.Errorf("%q mentions @pi", text)
		}
	}
}

func TestMentionsARenamedAgentInsteadOfPi(t *testing.T) {
	for _, text := range []string{"@claude", "hey @Claude, look", "(@CLAUDE)"} {
		if !MentionsAgent(text, "Claude") {
			t.Errorf("%q does not mention @claude", text)
		}
	}
	for _, text := range []string{"@claudette", "me@claude", "Claude", "@pi", "@claude-bot"} {
		if MentionsAgent(text, "Claude") {
			t.Errorf("%q mentions @claude", text)
		}
	}
	if MentionToken("Claude") != "@claude" {
		t.Fatal(MentionToken("Claude"))
	}
}

// spokenMention has no Pi test of its own in chat-mode.test.ts (the voice tests use it): these pin its documented cases.
func TestSpokenMention(t *testing.T) {
	for text, want := range map[string]string{
		"Hey Pi, can you help":   "@pi Hey Pi, can you help",
		"what do you think, Pi?": "@pi what do you think, Pi?",
		"can Pi summarize this":  "@pi can Pi summarize this",
		"@pi already":            "@pi already",
		"pie":                    "pie",
		"piano":                  "piano",
		"Pi-hole":                "Pi-hole",
		"Pi's answer was odd":    "Pi's answer was odd",
		"Pi’s answer":            "Pi’s answer",
	} {
		if got := SpokenMention(text, DefaultAgentName); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
}
