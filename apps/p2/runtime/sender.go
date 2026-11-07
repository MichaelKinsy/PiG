package runtime

import "strings"

// Ports apps/p2/src/runtime/sender.ts: the first block of every model message a person, a peer session or a subagent
// sends: `<sender kind="owner" name="Mario"/>`. The system prompt tells the model to attribute input by it; nothing
// reads it back, readers use the entry data.

// Sender is who sent an input, as the model reads it: OwnerSender, GuestSender, SessionSender or SubagentSender.
// Attributes returns the attributes in the order upstream's object literals list them.
type Sender interface{ attributes() [][2]string }

// OwnerSender is the owner.
type OwnerSender struct{ Name string }

// GuestSender is a guest.
type GuestSender struct{ ID, Name string }

// SessionSender is a peer session.
type SessionSender struct{ ID, Title string }

// SubagentSender is a subagent.
type SubagentSender struct{ Name string }

func (s OwnerSender) attributes() [][2]string {
	return [][2]string{{"kind", "owner"}, {"name", s.Name}}
}
func (s GuestSender) attributes() [][2]string {
	return [][2]string{{"kind", "guest"}, {"id", s.ID}, {"name", s.Name}}
}
func (s SessionSender) attributes() [][2]string {
	return [][2]string{{"kind", "session"}, {"id", s.ID}, {"title", s.Title}}
}
func (s SubagentSender) attributes() [][2]string {
	return [][2]string{{"kind", "subagent"}, {"name", s.Name}}
}

// SenderPrefix is how a sender block starts; the fake provider strips it from the text it echoes.
const SenderPrefix = "<sender "

// SenderText returns the sender block of sender, its values escaped: names and titles are chosen by people.
func SenderText(sender Sender) string {
	var out strings.Builder
	out.WriteString(SenderPrefix)
	for i, attribute := range sender.attributes() {
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(attribute[0])
		out.WriteString(`="`)
		out.WriteString(escapeAttribute(attribute[1]))
		out.WriteByte('"')
	}
	out.WriteString("/>")
	return out.String()
}

// AuthorSender returns the sender block's sender of a person's input.
func AuthorSender(author Author) Sender {
	if author.Kind == AuthorOwner {
		return OwnerSender{Name: author.Name}
	}
	return GuestSender{ID: author.ID, Name: author.Name}
}

var attributeEscaper = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")

// escapeAttribute is upstream's chain of replaceAll calls; one pass of the replacer is the same function because no
// replacement text contains a character that a later replaceAll replaces again except "&", which is replaced first.
func escapeAttribute(value string) string { return attributeEscaper.Replace(value) }
