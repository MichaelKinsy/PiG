package voice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/voice/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Ports apps/p2/plugins/voice/worker.ts: voice mode (worker): the owner's switch and voice, kept in a Durable document, and
// the prompt section voice mode turns on. A read/write guest's microphone switch is not written there: the document is the
// owner's, and a guest turning its microphone off must not turn voice mode off for anyone else. It counts while the guest's
// object lives (objects are per grant; leaving, a revoked grant or the linger time ends it): the prompt section is on while
// the owner's switch or any guest's is, since that guest talks by voice too.

// VoiceChoice is the conversation's voice (`VoiceChoiceSchema`).
type VoiceChoice struct {
	Engine   string  `json:"engine"`
	Voice    string  `json:"voice"`
	Speed    float64 `json:"speed"`
	Language *string `json:"language,omitempty"`
}

// VoiceModeState is the owner's switch and voice.
type VoiceModeState struct {
	Enabled bool         `json:"enabled"`
	Voice   *VoiceChoice `json:"voice"`
}

// VoiceModeDoc is the owner's switch and voice, stored per agent (`AgentConversation`: the main agent's on conversation 1,
// whichever `/tree` branch it works in; a new subagent starts without voice).
var VoiceModeDoc = durable.DefineDoc(durable.DocDefinition[VoiceModeState]{
	CommonDocDefinition: durable.CommonDocDefinition[VoiceModeState]{
		Kind:    "voice.mode",
		Version: 2,
		Initial: func() VoiceModeState { return VoiceModeState{} },
		// Version 1 had the switch only.
		Migrate: func(value durable.JsonObject, _ int) (VoiceModeState, error) {
			return VoiceModeState{Enabled: value.Value("enabled") == true}, nil
		},
		CheckpointWhen: func(VoiceModeState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})

// VoicePrompt is the spoken style the voice section asks for.
var VoicePrompt = strings.Join([]string{
	"The user is talking to you by voice: their messages are transcribed speech (expect recognition errors; read them",
	"charitably), and your text replies are read aloud sentence by sentence. Reply in short, plain spoken sentences: no",
	"Markdown, headings, lists, tables, code blocks, URLs or long file paths; say what matters and offer details on",
	"request.",
	"When you are about to use tools, first say in one short sentence what you are about to do: the user cannot see your",
	"screen, and silence while you work is confusing. A direct answer needs no announcement.",
	"Stay responsive: you are the voice, not the worker. Hand anything that takes more than a few seconds (coding,",
	"commands, research, multi-step work) to a doer subagent with the subagent tool: spawn one named doer with a",
	"complete brief, or send the next task to the existing doer. Then say in one sentence what you handed off, and end",
	"your turn. Do quick lookups yourself. When the doer reports, tell the user the outcome briefly. When the user asks",
	"how it is going, check with subagent status or answer. Steer the doer with subagent send; when the user wants it",
	"stopped or it is going wrong, stop it with subagent stop.",
}, " ")

var (
	guestMu sync.Mutex
	// guestVoices are the guests whose voice mode is on, per conversation of this worker (by principal key). Not durable on
	// purpose.
	guestVoices = map[string]map[string]struct{}{}
)

func setGuestVoice(conversation, guest string, on bool) {
	guestMu.Lock()
	defer guestMu.Unlock()
	guests := guestVoices[conversation]
	if guests == nil {
		guests = map[string]struct{}{}
	}
	if on {
		guests[guest] = struct{}{}
	} else {
		delete(guests, guest)
	}
	if len(guests) > 0 {
		guestVoices[conversation] = guests
	} else {
		delete(guestVoices, conversation)
	}
}

func guestsSpeak(conversation string) bool {
	guestMu.Lock()
	defer guestMu.Unlock()
	_, on := guestVoices[conversation]
	return on
}

// languageName is a language's English name ("German" for "de"), for the prompt; the tag itself where the runtime has none.
func languageName(tag string) string {
	parsed, err := language.Parse(tag)
	if err != nil {
		return tag
	}
	if name := display.English.Languages().Name(parsed); name != "" {
		return name
	}
	return tag
}

// VoicePromptFor is the voice section: the spoken style, and the language to answer in when the owner chose one other than
// English.
func VoicePromptFor(language *string) string {
	if language == nil || *language == "" || strings.ToLower(strings.SplitN(*language, "-", 2)[0]) == "en" {
		return VoicePrompt
	}
	name := languageName(*language)
	return fmt.Sprintf("%s Speak %s: reply in %s, since your replies are read aloud in %s.", VoicePrompt, name, name, name)
}

const extensionName = "p2-voice"

func voiceSection() *durable.PromptSection {
	return harness.Section("voice", func(ctx context.Context, input durable.PromptInput) (*string, error) {
		holder, err := p2runtime.AgentConversation(ctx, input.Read, input.ConversationId)
		if err != nil {
			return nil, err
		}
		doc, err := durable.Snapshot(ctx, input.Read, VoiceModeDoc, holder)
		if err != nil {
			return nil, err
		}
		if !guestsSpeak(fmt.Sprint(int64(input.ConversationId))) && (doc == nil || !doc.Enabled) {
			return nil, nil
		}
		var chosen *string
		if doc != nil && doc.Voice != nil {
			chosen = doc.Voice.Language
		}
		text := VoicePromptFor(chosen)
		return &text, nil
	})
}

// guestSwitch is a read/write guest's own microphone switch (the owner's is the document's).
var guestSwitch = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.voice-mode.guest", documents.StateOptions{
		Schema:  p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("enabled", schema.Bool())})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("enabled", false) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

func object(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func invalidArgs(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: message}, nil)
}

var workerFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.Contribute(facet, "p2.voice-extension", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extensionName, &p2runtime.P2Extension{
			Extension: &durable.Extension{Name: extensionName, Sections: []*durable.PromptSection{voiceSection()}},
		}))
		return nil
	}); err != nil {
		return err
	}
	linger := linger
	return facet.Serve(contract.VoiceMode, host.RemoteFactory(serveVoiceMode), host.ServeOptions{Linger: &linger})
})

func serveVoiceMode(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	session, _ := params.Value("session").(string)
	conversation, _ := params.Value("conversation").(float64)
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.CanSend(who, session) {
		return contracts.Implementation{}, p2runtime.Forbidden("participants only")
	}
	id := durable.ConversationId(conversation)
	harnessOf := p2runtime.HarnessOf(self)
	durableOf := func() (harness.Harness, error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		return parts.Durable()
	}
	// holder is where the agent's voice mode is (the main agent's on conversation 1); found when the document is followed.
	var holder atomic.Int64
	holder.Store(int64(id))
	// The document is the truth, followed: a commit below reaches it before the method returns.
	doc := self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[VoiceModeState]) (durable.Observation[VoiceModeState], error) {
		d, err := durableOf()
		if err != nil {
			return nil, err
		}
		found, err := d.Conversation(ctx, id)
		if err != nil {
			return nil, err
		}
		if found == nil {
			return nil, fmt.Errorf("Unknown conversation %d", id)
		}
		where, err := p2runtime.AgentConversation(ctx, d, id)
		if err != nil {
			return nil, err
		}
		holder.Store(int64(where))
		observed, err := durable.ObserveDoc(ctx, d, VoiceModeDoc, observer, where)
		if err != nil || observed != nil {
			return observed, err
		}
		// Nobody switched it yet: an absent address cannot be followed, so it is created as it reads (off, no voice).
		if _, err := d.Commit(ctx, func(tx durable.Tx) (any, error) {
			_, err := durable.TxDoc(tx, VoiceModeDoc, where)
			return nil, err
		}); err != nil {
			return nil, err
		}
		observed, err = durable.ObserveDoc(ctx, d, VoiceModeDoc, observer, where)
		if err == nil && observed == nil {
			err = fmt.Errorf("voice mode of conversation %d: its document is absent", id)
		}
		return observed, err
	}))
	commit := func(ctx context.Context, change func(doc *delta.Object) error) (delta.JsonValue, error) {
		d, err := durableOf()
		if err != nil {
			return nil, err
		}
		chordctx.Suspend(ctx)
		found, err := d.Conversation(ctx, id)
		if err != nil {
			return nil, err
		}
		if found == nil {
			return nil, invalidArgs(fmt.Sprintf("Unknown conversation %d", id))
		}
		_, err = d.Commit(ctx, func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc(tx, VoiceModeDoc, durable.ConversationId(holder.Load()))
			if err != nil {
				return nil, err
			}
			return nil, change(draft)
		})
		return nil, err
	}
	voice := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) { return object(values[0]).Value("voice"), nil })
	if !p2runtime.IsOwner(who) {
		// A read/write guest: its own microphone switch, counted while this object lives.
		key := p2runtime.Key(who)
		own := self.State(guestSwitch).(documents.SingletonHandle)
		self.Open(
			func(context.Context) (any, error) { return key, nil },
			func(any, host.NativeCloseReason, context.Context) error {
				setGuestVoice(fmt.Sprint(int64(id)), key, false)
				return nil
			},
		)
		return contracts.Implementation{Variant: "participant", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"enabled": self.Derive([]any{own}, func(values []any) (delta.JsonValue, error) { return object(values[0]).Value("enabled"), nil }),
				"voice":   voice,
			},
			Methods: map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					on := object(args).Value("enabled") == true
					if on {
						d, err := durableOf()
						if err != nil {
							return nil, err
						}
						chordctx.Suspend(ctx)
						found, err := d.Conversation(ctx, id)
						if err != nil {
							return nil, err
						}
						if found == nil {
							return nil, invalidArgs(fmt.Sprintf("Unknown conversation %d", id))
						}
					}
					setGuestVoice(fmt.Sprint(int64(id)), key, on)
					_, err := own.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("enabled", on) })
					return nil, err
				},
			},
		}}, nil
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"enabled": self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) { return object(values[0]).Value("enabled"), nil }),
			"voice":   voice,
		},
		Methods: map[string]contracts.MethodFunction{
			"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				on := object(args).Value("enabled") == true
				return commit(ctx, func(doc *delta.Object) error { return doc.Set("enabled", on) })
			},
			"choose": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return commit(ctx, func(doc *delta.Object) error { return doc.Set("voice", args) })
			},
		},
	}}, nil
}
