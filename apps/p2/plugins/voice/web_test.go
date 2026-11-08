package voice

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// piWebCases are the cases of test/p2/voice.test.ts that run the browser's own TypeScript (voice.web.test.ts), by suite and
// title: Pi's, none dropped.
var piWebCases = []struct{ suite, title string }{
	{"voice speech text", "reads streaming Markdown sentence by sentence, without code blocks, tables or syntax"},
	{"voice speech text", "waits for a sentence to end before reading it, and for a code fence to close"},
	{"voice speech text", "recognizes stop commands, not sentences containing stop"},
	{"voice transcripts waiting for the conversation", "sends one at a time in the order spoken, stopping the busy run before each, as the Stop button"},
	{"voice transcripts waiting for the conversation", "sends this person's queued messages that a stop withdrew with the new one, not lost"},
	{"voice transcripts waiting for the conversation", "does not stop again when a message is sent again after a link failure"},
	{"voice transcripts waiting for the conversation", "keeps transcripts while the link is down and sends them once through the new reference"},
	{"voice transcripts waiting for the conversation", "drops a refused transcript with an error, and the oldest waiting beyond the limit"},
	{"voice barge-in detector (pibot's)", "streams normally only while listening or hearing, and buffers for barge-in otherwise"},
	{"voice barge-in detector (pibot's)", "takes the playback's own echo for echo while speaking"},
	{"voice barge-in detector (pibot's)", "interrupts after five frames of sound the playback does not explain, then streams until reset"},
	{"voice interruptions", "listens for barge-in only while the agent's speech plays"},
	{"voice interruptions", "a message sent while the agent works stops it first, unless the speaker's words stopped it already"},
	{"voice interruptions", "waits for a stop in flight before sending, and sends the messages it withdrew with the next one"},
	{"voice interruptions", "keeps messages a barge-in's stop withdrew: with the next message, or alone after a while"},
	{"voice interruption by words", "tells words from noise, fillers and the agent's own speech heard back"},
	{"voice interruption by words", "drops a barge-in the room never hears as speech: the agent keeps speaking and working"},
	{"voice interruption by words", "drops a barge-in whose speech has no words (wipers): nothing cut, nothing sent"},
	{"voice interruption by words", "interrupts at the first words: speech cut and run stopped once, then the final is the message"},
	{"voice interruption by words", "stops a silently working agent at the first words, without waiting for the end of the utterance"},
	{"voice interruption by words", "interrupts at the final when the service sends no interim transcripts"},
	{"voice interruption by words", "sends a dictation the service split as one message, never stopping the run of its first half"},
	{"voice interruption by words", "in chat mode cuts the speech for anyone's words, and stops the run only when they are for the agent"},
	{"voice interruption by words", "forgets the utterances in progress when the room's connection ends, returning what was held"},
	{"voice microphone resampling", "keeps every sample across frames at 48 and 44.1 kHz"},
	{"voice answers read while they stream", "reads the text before a tool call without waiting for the call's arguments"},
	{"voice room frames", "splits a binary frame into kind, ID and PCM past the pad byte, copied to a buffer of its own"},
	{"voice in chat mode", "turns the agent's name spoken anywhere into a mention in front, keeping what was said"},
	{"voice in chat mode", "does not stop a busy run for a message that is not for Pi"},
	{"voice choice against the service", "keeps a language the engine speaks, else falls back to its default; lists English and Deutsch first"},
}

// The browser's half of voice mode is Pi's TypeScript, carried verbatim; Pi's cases of it run on those sources in Node, each
// under its Pi title, and each must run and pass (a skipped or missing case fails).
func TestVoiceBrowserCases(t *testing.T) {
	ran := map[string]oracle.NodeCase{}
	for _, test := range oracle.RunNodeCases(t, "plugins/voice/voice.web.test.ts") {
		ran[test.Title] = test
		if test.Nesting == 0 && !strings.Contains(test.Title, " > ") && !test.OK {
			t.Errorf("%s failed: %s", test.Title, test.Error)
		}
	}
	for _, want := range piWebCases {
		title := want.suite + " > " + want.title
		t.Run(want.suite+"/"+want.title, func(t *testing.T) {
			test, ok := ran[title]
			switch {
			case !ok:
				t.Fatalf("Node did not run %q", title)
			case test.Skip || test.Todo:
				t.Fatalf("%q was skipped", title)
			case !test.OK:
				t.Fatal(test.Error)
			}
		})
		delete(ran, title)
	}
	for title, test := range ran {
		if strings.Contains(title, " > ") {
			t.Errorf("Node ran %q, which is not one of Pi's cases (ok=%v)", title, test.OK)
		}
	}
}
