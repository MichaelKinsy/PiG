// Package contract ports apps/p2/plugins/voice/contract.ts and protocol.ts: voice mode of one conversation, served by each
// session worker. One voice per conversation, the owner's: the owner switches voice mode (the agent's prompt then asks for
// short spoken answers) and chooses the engine, voice and speed everyone hears. A read/write guest switches only its own
// microphone (its transcripts are its messages) and sees the owner's choice; it never changes what the owner set.
package contract

import (
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
	c    = p2runtime.Contract
	str  = schema.Str
	num  = schema.Num
	nul  = schema.Nul
	prop = schema.Prop
	opt  = schema.Opt
	obj  = func(properties schema.Properties, options ...schema.ObjectOption) schema.Schema {
		return schema.Obj(properties, options...)
	}
	arr = func(item schema.Schema, options ...schema.ArrayOption) schema.Schema {
		return schema.Arr(item, options...)
	}
	union  = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	lit    = func(value string) schema.Schema { return p2runtime.Literal(value) }
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

// VoiceChoiceSchema is the conversation's voice: an engine of the speech service, one of its voices, and the speed; and the
// language it speaks, when the engine offers several (absent: the engine's default).
var VoiceChoiceSchema = obj(schema.Properties{
	prop("engine", id),
	prop("voice", id),
	prop("speed", num(schema.Minimum(0.5), schema.Maximum(2))),
	opt("language", LanguageSchema),
})

// VoiceMode is the voice mode of one conversation as a browser holds it.
//
// 3: the voice carries a language. A state schema change needs a new contract version: objects of the old one keep sending
// the old shape (wire frames follow the schema), so the reload must replace them.
var VoiceMode = must(contracts.Remote("p2.voice-mode", contracts.RemoteDeclaration{
	Version: 3,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()), contracts.Prop("conversation", contracts.Int()),
	},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				// The conversation's voice mode (the prompt section).
				p2runtime.StateProp("enabled", schema.Bool()),
				// The chosen voice; null until the owner chose one (the service's default then).
				p2runtime.StateProp("voice", union(VoiceChoiceSchema, nul())),
			},
			Methods: []contracts.MethodEntry{
				contracts.Meth("set", method(obj(schema.Properties{prop("enabled", schema.Bool())}))),
				contracts.Meth("choose", method(VoiceChoiceSchema)),
			},
		}),
		// A read/write guest.
		contracts.Var("participant", contracts.VariantDeclaration{
			State: []contracts.Property{
				// This guest's own microphone switch.
				p2runtime.StateProp("enabled", schema.Bool()),
				// The owner's choice, read only.
				p2runtime.StateProp("voice", union(VoiceChoiceSchema, nul())),
			},
			Methods: []contracts.MethodEntry{contracts.Meth("set", method(obj(schema.Properties{prop("enabled", schema.Bool())})))},
		}),
	},
}))

// serviceState are the members the speech service's two contracts share.
func serviceState() []contracts.Property {
	return []contracts.Property{
		// The speech service's engines and default; null while voice.json names none (or it is invalid).
		p2runtime.StateProp("voices", union(VoicesSchema, nul())),
		// Why voice.json names a service but its voices are unknown (the service unreachable); null otherwise.
		p2runtime.StateProp("failure", union(str(), nul())),
	}
}

// VoiceServiceStore is the server's speech service (server, in memory): `voice.json` watched while anyone holds this, its
// service's voices fetched again whenever the file changes. Never the URL or token. `refresh` asks the service again.
//
// 2: engines list their languages (see VoiceMode's version).
var VoiceServiceStore = func() *contracts.Contract {
	var members []contracts.LocalEntry
	for _, state := range serviceState() {
		members = append(members, contracts.Mem(state.Name, state.Value))
	}
	members = append(members, contracts.Mem("refresh", contracts.NewMethod()))
	return must(contracts.Local("p2.voice-service-store", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: members}))
}()

// VoiceService is whether voice mode is available, for those who may talk: the owner and read/write guests.
//
// 2: engines list their languages (see VoiceMode's version).
var VoiceService = must(contracts.Remote("p2.voice-service", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants:    []contracts.VariantEntry{contracts.Var("talker", contracts.VariantDeclaration{State: serviceState()})},
}))
