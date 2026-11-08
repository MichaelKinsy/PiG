// Package contract ports apps/p2/plugins/discord/contract.ts and the Discord relay (relay.ts): Pi on Discord. The owner's
// bots run on the p2 server; a session linked to a channel through one of them gets, at a mention by someone allowed to
// wake Pi, the messages since the link's cursor as one input, and its answer goes back as a reply. A thread of a linked
// channel gets a subagent forked from the main conversation.
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
	obj  = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	arr  = func(item schema.Schema, options ...schema.ArrayOption) schema.Schema {
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

// DiscordBatch is the entry kind of a delivered batch: a person's message (its data is message data) with the Discord details.
const DiscordBatch = "discord.batch"

// WakeSchema is who may wake Pi in a channel: anyone, or the listed Discord user and role IDs.
var WakeSchema = obj(schema.Properties{
	prop("anyone", schema.Bool()),
	prop("users", arr(str(schema.MaxLength(32)), schema.MaxItems(100))),
	prop("roles", arr(str(schema.MaxLength(32)), schema.MaxItems(100))),
})

// ThreadBindingSchema is a Discord thread of a linked channel: its subagent conversation and its own cursor.
var ThreadBindingSchema = obj(schema.Properties{
	prop("conversation", schema.Int(schema.Minimum(1))), prop("name", str()), prop("cursor", union(str(), nul())),
})

// BindingSchema is one channel linked to one session, through one bot. A session has at most one, a channel at most one.
var BindingSchema = obj(schema.Properties{
	prop("botId", str()), prop("channelId", str()), prop("channelName", str()), prop("guildId", str()), prop("guildName", str()),
	prop("session", str()), prop("wake", WakeSchema),
	// The last Discord message delivered (null: none yet, the next batch starts with the newest 50).
	prop("cursor", union(str(), nul())),
	prop("threads", p2runtime.Record(ThreadBindingSchema)),
	prop("createdAt", num()),
})

// PendingSchema is a delivered batch waiting for its turn's answer, to go back as a reply to the message that woke Pi.
var PendingSchema = obj(schema.Properties{
	prop("botId", str()), prop("session", str()), prop("channelId", str()), prop("replyChannelId", str()),
	prop("messageId", str()), prop("submissionId", num()), prop("since", num()),
})

// GuildSchema is a Discord server (guild).
var GuildSchema = obj(schema.Properties{prop("id", str()), prop("name", str())})

// BotStatusSchema is a bot's connection as the owner sees it.
var BotStatusSchema = union(
	obj(schema.Properties{prop("state", lit("connecting"))}),
	obj(schema.Properties{prop("state", lit("error")), prop("error", str())}),
	obj(schema.Properties{
		prop("state", lit("connected")), prop("user", str()), prop("applicationId", str()), opt("avatarUrl", str()),
		prop("inviteUrl", str()), prop("guilds", arr(GuildSchema)),
	}),
)

// BotSchema is one configured bot: its ID (stable, p2's own) and its connection.
var BotSchema = obj(schema.Properties{prop("id", str()), prop("status", BotStatusSchema)})

var namedSchema = obj(schema.Properties{prop("id", str()), prop("name", str())})

// ServerDetailsSchema is a Discord server's text channels and roles, as the link form picks them.
var ServerDetailsSchema = obj(schema.Properties{prop("channels", arr(namedSchema)), prop("roles", arr(namedSchema))})

// LinkSchema is linking a session to a channel: the bot and channel, who may wake Pi, the sandbox the session moves into
// and its lock.
var LinkSchema = obj(schema.Properties{
	prop("session", str()), prop("bot", str()), prop("guild", str()), prop("channel", str()), prop("wake", WakeSchema),
	prop("sandbox", str()), prop("lock", p2runtime.SessionLockSchema),
})

// LinkUpdateSchema is what a link's owner may change in place: who may wake Pi, and what the session may use.
var LinkUpdateSchema = obj(schema.Properties{prop("session", str()), prop("wake", WakeSchema), prop("lock", p2runtime.SessionLockSchema)})

var token = str(schema.MinLength(10), schema.MaxLength(200))

func ownerMethods() []contracts.MethodEntry {
	return []contracts.MethodEntry{
		contracts.Meth("addBot", method(obj(schema.Properties{prop("token", token)}), obj(schema.Properties{prop("id", str())}))),
		contracts.Meth("setBotToken", method(obj(schema.Properties{prop("bot", str()), prop("token", token)}))),
		contracts.Meth("removeBot", method(obj(schema.Properties{prop("bot", str())}))),
		contracts.Meth("server", method(obj(schema.Properties{prop("bot", str()), prop("guild", str())}), ServerDetailsSchema)),
		contracts.Meth("mcpServers", contracts.NewMethod(contracts.Schema{}, c(arr(str())))),
		contracts.Meth("link", method(LinkSchema)),
		contracts.Meth("unlink", method(obj(schema.Properties{prop("session", str())}))),
	}
}

// DiscordStore is the bots, the links and the relay between Discord and sessions (server, host-internal, host lifetime).
var DiscordStore = func() *contracts.Contract {
	members := []contracts.LocalEntry{
		contracts.Mem("bots", c(arr(BotSchema))), contracts.Mem("bindings", c(arr(BindingSchema))),
	}
	for _, entry := range ownerMethods() {
		members = append(members, contracts.Mem(entry.Name, entry.Value))
	}
	members = append(members, contracts.Mem("updateLink", method(LinkUpdateSchema)))
	return must(contracts.Local("p2.discord-store", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: members}))
}()

// DiscordV2 is the owner's bots and links, version 2.
var DiscordV2 = must(contracts.Remote("p2.discord", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
		State: []contracts.Property{
			p2runtime.StateProp("bots", arr(BotSchema)), p2runtime.StateProp("bindings", arr(BindingSchema)),
		},
		Methods: ownerMethods(),
	})},
}))

// Commands are the channel commands: a mention of the bot whose remaining text is exactly one of these (an optional
// leading slash).
var Commands = []string{"stop", "status", "compact"}

var commandSchema = union(lit("stop"), lit("status"), lit("compact"))

// OfferedFileSchema is a file the agent offered in a turn (`send_file`): the session's download, to attach to the reply.
var OfferedFileSchema = obj(schema.Properties{prop("id", str()), prop("name", str()), prop("size", num())})

// Discord is the owner's bots and links. 3: `updateLink`. The provider still serves 2 (its `accepts`).
var Discord = must(contracts.Evolve(DiscordV2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.AddMethod("owner", "updateLink", c(LinkUpdateSchema), c(nul()))
}))

// DiscordFileSchema is a Discord attachment to download into the session's uploads (at most 25 MiB; larger ones are links).
var DiscordFileSchema = obj(schema.Properties{
	prop("url", str()), prop("name", str()), prop("size", num()), prop("key", str()), prop("ref", str()),
})

// DiscordIngress is what the server hands a session's worker (server only): batches, finished turns, threads.
var DiscordIngress = must(contracts.Remote("p2.discord-ingress", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{contracts.Var("server", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
		// The session is no longer linked: its agents' Discord prompt section goes.
		contracts.Meth("unlinked", contracts.NewMethod()),
		contracts.Meth("deliver", method(
			obj(schema.Properties{
				opt("conversation", schema.Int(schema.Minimum(1))),
				prop("requestId", str(schema.MaxLength(200))),
				prop("author", namedSchema),
				prop("text", str(schema.MaxLength(200000))),
				prop("context", obj(schema.Properties{
					prop("channel", str()), prop("guild", str()), prop("messages", schema.Int()), opt("thread", str()),
				})),
				prop("files", arr(DiscordFileSchema, schema.MaxItems(p2runtime.MaxAttachments))),
				prop("lock", union(p2runtime.SessionLockSchema, nul())),
			}),
			obj(schema.Properties{prop("conversation", schema.Int()), prop("submissionId", num())}),
		)),
		contracts.Meth("turn", method(
			obj(schema.Properties{prop("submissionId", num())}),
			union(
				obj(schema.Properties{prop("state", lit("pending"))}),
				obj(schema.Properties{prop("state", lit("answered")), prop("text", str()), prop("files", arr(OfferedFileSchema))}),
				obj(schema.Properties{prop("state", lit("stopped"))}),
				obj(schema.Properties{prop("state", lit("unanswered")), prop("reason", str()), prop("files", arr(OfferedFileSchema))}),
			),
		)),
		contracts.Meth("read", method(
			obj(schema.Properties{
				prop("id", str()), prop("offset", schema.Int(schema.Minimum(0))),
				prop("length", schema.Int(schema.Minimum(1), schema.Maximum(4*1024*1024))),
			}),
			schema.Bytes(),
		)),
		contracts.Meth("command", method(
			obj(schema.Properties{opt("conversation", schema.Int(schema.Minimum(1))), prop("command", commandSchema)}),
			obj(schema.Properties{prop("text", str())}),
		)),
		contracts.Meth("thread", method(
			obj(schema.Properties{prop("name", str(schema.MaxLength(100))), prop("before", num())}),
			obj(schema.Properties{prop("conversation", schema.Int())}),
		)),
	}})},
}))
