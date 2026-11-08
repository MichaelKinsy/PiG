// Package contract ports apps/p2/plugins/chat-mode/contract.ts: chat mode, where the people viewing a conversation talk to
// each other in its transcript and the agent reads along only when someone mentions it (a leaf package other plugins
// import for the contracts, their schemas and the mention rules). Served by each session worker.
package contract

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"regexp"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	c       = p2runtime.Contract
	str     = schema.Str
	num     = schema.Num
	boolean = schema.Bool
	prop    = schema.Prop
	opt     = schema.Opt
	obj     = func(properties schema.Properties, options ...schema.ObjectOption) schema.Schema {
		return schema.Obj(properties, options...)
	}
	arr    = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	union  = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

// compileCheck compiles a schema for repeated `compile(schema).check` calls; the schemas here are constants.
func compileCheck(s schema.Schema) func(value any) bool {
	validator, err := schema.Compile(s, schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return func(value any) bool {
		ok, err := validator.Check(value)
		return err == nil && ok
	}
}

// ChatEntry is the entry kind of a chat message: display-only, it carries no model message.
const ChatEntry = "chat.message"

// MentionEntry is the entry kind of a mention: an input whose model message is the digest, and whose data is what the person
// wrote.
const MentionEntry = "chat.mention"

// ReplyToSchema is the message a reply answers: an entry of the same conversation, with its author and the start of its
// text. The worker derives author and excerpt from that entry; a client only names the entry. Pi's answers can be replied to.
var ReplyToSchema = obj(schema.Properties{
	prop("entryId", schema.Int(schema.Minimum(1))),
	prop("author", obj(schema.Properties{
		prop("kind", union(p2runtime.Literal("owner"), p2runtime.Literal("guest"), p2runtime.Literal("agent"))),
		prop("id", str()),
		prop("name", str()),
	}, schema.Open())),
	prop("excerpt", str()),
}, schema.Open())

// ChatMessageSchema is a chat entry's data: a person's message (author stamped by the worker, attachments as the session's
// upload IDs), perhaps answering another.
var ChatMessageSchema = obj(append(append(schema.Properties{}, p2runtime.PropertiesOf(p2runtime.MessageDataSchema)...),
	opt("replyTo", ReplyToSchema)), schema.Open())

// MentionSchema is a mention entry's data: what the person wrote, and the IDs of the chat entries its digest passed on to Pi.
var MentionSchema = obj(append(append(schema.Properties{}, p2runtime.PropertiesOf(ChatMessageSchema)...),
	prop("included", arr(schema.Int()))), schema.Open())

var (
	isReplyTo     = compileCheck(ReplyToSchema)
	isChatMessage = compileCheck(ChatMessageSchema)
	isMention     = compileCheck(MentionSchema)
)

// IsReplyTo reports whether value is a reply's quote (`ReplyToSchema`).
func IsReplyTo(value any) bool { return isReplyTo(value) }

// IsChatMessage reports whether data is a chat message's data. Entries are history: read their data defensively, never assume
// this code wrote it.
func IsChatMessage(data any) bool { return isChatMessage(data) }

// IsMention reports whether data is a mention's data.
func IsMention(data any) bool { return isMention(data) }

// DefaultAgentName is the name people call the agent by in a conversation (chat mode's setting): one word, as it is spoken
// and mentioned ("Pi", "Claude"). Letters, digits, `_` and `-`, starting with a letter.
const DefaultAgentName = "Pi"

// AgentNameSchema is the agent's name.
var AgentNameSchema = str(schema.Pattern("^[A-Za-z][A-Za-z0-9_-]{0,31}$"))

// MentionToken is the mention of the agent called name: `@` and the name in lower case ("@pi", "@claude").
func MentionToken(name string) string { return "@" + strings.ToLower(name) }

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

// MentionsAgent is whether text holds `@<name>` in any case, as a word of its own: with the default, "@pi", "@Pi," and
// "(@PI)" mention; "@pizza" and "me@pi" do not.
func MentionsAgent(text, name string) bool {
	name = withDefault(name)
	for _, at := range foldedAt(text, "@"+name) {
		before, hasBefore := lastRune(text[:at.start])
		after, hasAfter := firstRune(text[at.end:])
		if (!hasBefore || !(isWordRune(before) || before == '@')) && (!hasAfter || !(isWordRune(after) || after == '-')) {
			return true
		}
	}
	return false
}

func withDefault(name string) string {
	if name == "" {
		return DefaultAgentName
	}
	return name
}

type span struct{ start, end int }

// foldedAt are the places of needle in text under simple case folding, as a case-insensitive `u` regular expression matches
// it: every starting position (the matches may overlap).
func foldedAt(text, needle string) []span {
	re := regexp.MustCompile(`^(?i:` + regexp.QuoteMeta(needle) + `)`)
	var found []span
	for start := range text {
		if loc := re.FindStringIndex(text[start:]); loc != nil {
			found = append(found, span{start, start + loc[1]})
		}
	}
	return found
}

func lastRune(text string) (rune, bool) {
	r, size := utf8.DecodeLastRuneInString(text)
	return r, size > 0
}

func firstRune(text string) (rune, bool) {
	r, size := utf8.DecodeRuneInString(text)
	return r, size > 0
}

// spokesName is whether text names the agent anywhere in an utterance, as speech recognition writes it (no "@"): a whole word
// in any case, whatever punctuation surrounds it. With the default: "Hey Pi, …", "can Pi summarize this", "what do you
// think, Pi?". Not "pie", "piano", "pip" or "Pi-hole"; and not "Pi's": a possessive or contraction talks about the agent more
// often than to it ("Pi's answer was odd"), and is cheap to say again.
func spokesName(text, name string) bool {
	for _, at := range foldedAt(text, name) {
		before, hasBefore := lastRune(text[:at.start])
		after, hasAfter := firstRune(text[at.end:])
		if (!hasBefore || !(isWordRune(before) || before == '\'' || before == '’' || before == '@' || before == '-')) &&
			(!hasAfter || !(isWordRune(after) || after == '\'' || after == '’' || after == '-')) {
			return true
		}
	}
	return false
}

// SpokenMention is a spoken message as chat mode reads it: one that names the agent gets its mention in front (what was said
// is kept whole, for the context: "@pi can Pi summarize this"). One already holding the mention stays as it is.
func SpokenMention(text, name string) string {
	name = withDefault(name)
	if MentionsAgent(text, name) {
		return text
	}
	if spokesName(text, name) {
		return MentionToken(name) + " " + strings.TrimFunc(text, isJSSpace)
	}
	return text
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// ChatModeSetting is one conversation's switch and agent name (`p2.chat-mode-setting`, worker-local), followed from its
// Durable document. 2: the agent's name. 3: `waiting`.
var ChatModeSetting = must(contracts.Local("p2.chat-mode-setting", contracts.LocalDeclaration{
	Version: 3,
	Params:  contracts.Params{contracts.Prop("conversation", contracts.Int())},
	Members: []contracts.LocalEntry{
		contracts.Mem("enabled", c(boolean())),
		contracts.Mem("name", c(AgentNameSchema)),
		// How many chat messages a mention would pass on now (the composer's count); 0 while chat mode is off.
		contracts.Mem("waiting", c(num())),
		contracts.Mem("set", method(obj(schema.Properties{prop("enabled", boolean())}))),
		contracts.Mem("rename", method(obj(schema.Properties{prop("name", AgentNameSchema)}))),
	},
}))

// ChatMode is chat mode of one conversation as a browser sees it: everyone reads it and the agent's name; only the owner
// switches it and renames the agent. 2: the agent's name (a state schema change needs a new contract version).
var ChatMode = must(contracts.Remote("p2.chat-mode", contracts.RemoteDeclaration{
	Version: 2,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()), contracts.Prop("conversation", contracts.Int()),
	},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: state()}),
		contracts.Var("owner", contracts.VariantDeclaration{State: state(), Methods: []contracts.MethodEntry{
			contracts.Meth("set", method(obj(schema.Properties{prop("enabled", boolean())}))),
			contracts.Meth("rename", method(obj(schema.Properties{prop("name", AgentNameSchema)}))),
		}}),
	},
}))

func state() []contracts.Property {
	return []contracts.Property{
		p2runtime.StateProp("enabled", boolean()), p2runtime.StateProp("waiting", num()), p2runtime.StateProp("name", AgentNameSchema),
	}
}
