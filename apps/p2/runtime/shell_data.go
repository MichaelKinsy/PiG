package runtime

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/schema"
)

// ShellDataSchema is the data of a `p2.shell` entry (shell.ts `ShellDataSchema`; kinds may add fields).
var ShellDataSchema = schema.Obj(schema.Properties{
	schema.Prop("author", AuthorSchema),
	schema.Prop("command", schema.Str()),
	schema.Prop("output", schema.Str()),
	schema.Opt("exitCode", schema.Num()),
	schema.Prop("cancelled", schema.Bool()),
	schema.Prop("truncated", schema.Bool()),
	schema.Opt("fullOutputPath", schema.Str()),
	schema.Prop("includeInContext", schema.Bool()),
	schema.Opt("error", schema.Str()),
}, schema.Open())

var isShellData = check(ShellDataSchema)

// ShellDataOf returns the shell data an entry's data records, if it is one (`shellData`).
func ShellDataOf(data any) (ShellData, bool) {
	if !isShellData(data) {
		return ShellData{}, false
	}
	object := data.(*delta.JsonObject)
	author := object.Value("author").(*delta.JsonObject)
	result := ShellData{
		Author:           Author{Kind: AuthorKind(text(author, "kind")), ID: text(author, "id"), Name: text(author, "name")},
		Command:          text(object, "command"),
		Output:           text(object, "output"),
		IncludeInContext: object.Value("includeInContext") == true,
		Cancelled:        object.Value("cancelled") == true,
		Truncated:        object.Value("truncated") == true,
	}
	if code, ok := object.Value("exitCode").(float64); ok {
		result.ExitCode = &code
	}
	if path, ok := object.Value("fullOutputPath").(string); ok {
		result.FullOutputPath = &path
	}
	if message, ok := object.Value("error").(string); ok {
		result.Error = &message
	}
	return result, true
}
