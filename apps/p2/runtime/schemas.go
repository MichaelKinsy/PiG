package runtime

import (
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/schemas.ts: schemas several plugins' contracts share (browser-safe). A contract imports
// only the API and its own plugin's files, so what two contracts both use lives here: attachments (files, chat), model
// references and thinking levels (sessions, chat), pages of a conversation's entries (chat, the clients' transcripts),
// directory listings (envs, locations) and artifact descriptions (artifacts, the transcript).

// AttachmentSchema is an attachment of a message: an upload of the session (its `Files`, space `uploads`) as written to
// the machine where the receiving conversation works (`path`), never into the project. `localPath` is the local upload
// copy, retained for inline images when path points to the remote host.
var AttachmentSchema = schema.Obj(schema.Properties{
	schema.Prop("id", schema.Str()),
	schema.Prop("name", schema.Str()),
	schema.Prop("path", schema.Str()),
	schema.Opt("localPath", schema.Str()),
	schema.Prop("size", schema.Num()),
	schema.Opt("mimeType", schema.Str()),
})

// MaxAttachments is the most attachments per message.
const MaxAttachments = 20

// ModelRefSchema is a model: its provider and its ID.
var ModelRefSchema = schema.Obj(schema.Properties{
	schema.Prop("provider", schema.Str(schema.MinLength(1))),
	schema.Prop("modelId", schema.Str(schema.MinLength(1))),
})

// ThinkingLevels are the thinking levels a session or conversation may ask for (clamped to the model when it runs), as
// Pi Mobile accepts.
var ThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// ThinkingLevelSchema is a string enum (pi-ai's `StringEnum` shape), validated wherever a thinking level is set.
var ThinkingLevelSchema = func() schema.Schema {
	levels := make([]any, len(ThinkingLevels))
	for i, level := range ThinkingLevels {
		levels[i] = level
	}
	return chordjson.ObjectOf("type", "string", "enum", levels)
}()

// IsThinkingLevel reports whether value is a thinking level.
func IsThinkingLevel(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	for _, level := range ThinkingLevels {
		if level == text {
			return true
		}
	}
	return false
}

// MaxEntriesPage is the most entries per `Chat.entries` page (a page is also cut at about 1 MB of entries).
const MaxEntriesPage = 200

// EntriesQuerySchema is a page of a conversation's entries (`Conversation.entries()`), for `ConversationTranscript`'s
// loader: the oldest and newest entry IDs that may be returned, the order (default `descending`, newest first) and the
// page size.
var EntriesQuerySchema = schema.Obj(schema.Properties{
	schema.Opt("minEntryId", schema.Int(schema.Minimum(0))),
	schema.Opt("maxEntryId", schema.Int(schema.Minimum(0))),
	schema.Opt("order", schema.Union([]schema.Schema{Literal("ascending"), Literal("descending")})),
	schema.Prop("limit", schema.Int(schema.Minimum(1), schema.Maximum(MaxEntriesPage))),
})

// EntriesPageSchema is a page of entries: Durable entry records in the query's order, and whether more entries match
// the query (the page was full, or cut at its byte bound).
var EntriesPageSchema = schema.Obj(schema.Properties{
	schema.Prop("items", schema.Arr(schema.Unknown())),
	schema.Prop("more", schema.Bool()),
})

// DirectoryListingSchema is the folders of a directory (this device's or an environment's), for the directory pickers
// and `location_dirs`.
var DirectoryListingSchema = schema.Obj(schema.Properties{
	schema.Prop("path", schema.Str()),
	schema.Prop("parent", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
	schema.Prop("directories", schema.Arr(schema.Str())),
	schema.Prop("truncated", schema.Bool()),
})

// ArtifactInfoSchema is one version of an artifact: the artifact tool's result details and the artifacts plugin's index
// entries.
var ArtifactInfoSchema = schema.Obj(schema.Properties{
	schema.Prop("name", schema.Str(schema.Pattern("^[a-z0-9][a-z0-9_-]{0,63}$"))),
	schema.Prop("title", schema.Str()),
	schema.Prop("type", schema.Union([]schema.Schema{Literal("html"), Literal("svg"), Literal("markdown"), Literal("text")})),
	schema.Prop("version", schema.Int(schema.Minimum(1))),
})
