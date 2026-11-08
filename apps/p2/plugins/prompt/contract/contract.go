// Package contract ports apps/p2/plugins/prompt/contract.ts: the session's prompt templates and skills for participants,
// served by the session worker (a leaf package other plugins import).
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

var c = p2runtime.Contract

// TemplateInfoSchema is a prompt template as participants see it. `source` is p2's data directory (`~/.pi/p2/prompts` on a
// remote environment) or the project's `.pi/prompts`.
var TemplateInfoSchema = schema.Obj(schema.Properties{
	schema.Prop("name", schema.Str()),
	schema.Prop("description", schema.Str()),
	schema.Opt("argumentHint", schema.Str()),
	schema.Prop("source", schema.Union([]schema.Schema{p2runtime.Literal("global"), p2runtime.Literal("project")})),
})

// SkillInfoSchema is a skill as participants see it.
var SkillInfoSchema = schema.Obj(schema.Properties{schema.Prop("name", schema.Str()), schema.Prop("description", schema.Str())})

// PromptResources: prompt templates (`/name args`) and skills (`/skill:name args`) where a session's conversation works
// (the session's location, or a subagent's own), read-only and reloaded when their files change (like AGENTS.md);
// `expand` turns a typed command into the text to send, on the worker, so templates and skills on a remote environment
// work and every client expands alike. Participants only (`p2.prompt-resources`; version 2: per conversation).
var PromptResources = must(contracts.Remote("p2.prompt-resources", contracts.RemoteDeclaration{
	Version: 2,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()),
		contracts.Prop("conversation", contracts.Int(contracts.Min(1))),
	},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("participant", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("templates", schema.Arr(TemplateInfoSchema)),
				p2runtime.StateProp("skills", schema.Arr(SkillInfoSchema)),
			},
			Methods: []contracts.MethodEntry{
				// pi's expansion: `/skill:name args` becomes the skill block plus the args, `/name args` the template with its
				// arguments substituted. Other text, unknown names and unreadable skills come back unchanged.
				contracts.Meth("expand", contracts.NewMethod(
					c(schema.Obj(schema.Properties{schema.Prop("text", schema.Str(schema.MaxLength(1_000_000)))})),
					c(schema.Obj(schema.Properties{
						schema.Prop("text", schema.Str()),
						schema.Opt("skill", schema.Obj(schema.Properties{schema.Prop("name", schema.Str())})),
					})),
				)),
			},
		}),
	},
}))
