package views

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports apps/p2/plugins/views/text.ts: a text room's model (apps/p2/docs/rooms.md, jot's id-based editing): the text and its element
// IDs (articulated's IdList, deleted IDs kept as tombstones), edits that name IDs instead of indices, and how each applies.
// The rules are deterministic functions of (state, edit): the server applies every edit and owns the state, and the view kit
// (Pi's TypeScript, vendored) applies the server's log the same way, so every replica that applies one log in order agrees.
// Go forced difference: a text is UTF-16 code units, as JavaScript's string indexes it (the IDs number code units).

// MaxText is the most a text room's text may hold, in UTF-16 code units: an insert past it is dropped.
const MaxText = 100_000

// growth: a delete whose range grew by more than this since it was made (text others inserted inside) is dropped.
const growth = 10

// TextID is an element ID as an edit carries it (a JSON number: not necessarily a safe integer).
type TextID struct {
	BunchID string
	Counter float64
}

// TextEdit is one edit: insert Content right after Before (nil: at the start), or delete what lies from Start to End.
type TextEdit struct {
	Insert bool
	// Insert: Before, ID, Content, InWord (typed inside a word: dropped when Before was deleted meanwhile).
	Before  *TextID
	ID      TextID
	Content string
	InWord  bool
	// Delete: Start, End, Length.
	Start, End TextID
	Length     float64
}

// TextDoc is a text and its IDs: present ID i is code unit i. Immutable: every change returns a new one.
type TextDoc struct {
	Text []uint16
	IDs  *IdList
}

// String is the text.
func (doc *TextDoc) String() string { return string(utf16.Decode(doc.Text)) }

// FreshText is a text with fresh IDs (one bunch).
func FreshText(text string, bunch string) *TextDoc {
	units := utf16.Encode([]rune(text))
	if len(units) > MaxText {
		units = units[:MaxText]
	}
	ids := NewIdList()
	if len(units) > 0 {
		ids, _ = ids.InsertAfter(nil, ElementID{BunchID: bunch}, int64(len(units)))
	}
	return &TextDoc{Text: units, IDs: ids}
}

// TextMarks are each editor's last batch (its watermark).
type TextMarks map[string]int64

func numbersOf(value any) ([]int64, bool) {
	list, ok := value.([]any)
	if !ok {
		return nil, false
	}
	numbers := make([]int64, len(list))
	for i, each := range list {
		number, ok := each.(float64)
		if !ok || !isSafeInteger(number) {
			return nil, false
		}
		numbers[i] = int64(number)
	}
	return numbers, true
}

// compactOf is the compact saved list in value, if it has the shape (its content is checked by LoadCompactIdList).
func compactOf(value any) (CompactSavedIdList, bool) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return CompactSavedIdList{}, false
	}
	var saved CompactSavedIdList
	list, ok := object.Value("bunchIds").([]any)
	if !ok {
		return saved, false
	}
	saved.BunchIDs = make([]string, len(list))
	for i, each := range list {
		if saved.BunchIDs[i], ok = each.(string); !ok {
			return saved, false
		}
	}
	if saved.SignedCounts, ok = numbersOf(object.Value("signedCounts")); !ok {
		return saved, false
	}
	if saved.StartCounterDeltas, ok = numbersOf(object.Value("startCounterDeltas")); !ok {
		return saved, false
	}
	if saved.StartCounterValues, ok = numbersOf(object.Value("startCounterValues")); !ok {
		return saved, false
	}
	return saved, len(saved.SignedCounts) == len(saved.BunchIDs)
}

// LoadText is the document stored in saved, if it is a valid one: a text within the limit, IDs that load and number exactly its
// characters, watermarks of safe integers. Anything else (an old shape, a damaged or forged document) is not ok.
func LoadText(saved delta.JsonValue) (*TextDoc, TextMarks, bool) {
	object, ok := saved.(*delta.JsonObject)
	if !ok {
		return nil, nil, false
	}
	text, isText := object.Value("text").(string)
	if !isText {
		return nil, nil, false
	}
	units := utf16.Encode([]rune(text))
	if len(units) > MaxText {
		return nil, nil, false
	}
	compact, ok := compactOf(object.Value("textIds"))
	if !ok {
		return nil, nil, false
	}
	marksObject, ok := object.Value("textMarks").(*delta.JsonObject)
	if !ok {
		return nil, nil, false
	}
	marks := TextMarks{}
	for editor, value := range marksObject.All() {
		mark, ok := value.(float64)
		if !ok || !isSafeInteger(mark) || mark < 0 {
			return nil, nil, false
		}
		marks[editor] = int64(mark)
	}
	ids, err := LoadCompactIdList(compact)
	if err != nil || ids.Length() != len(units) {
		return nil, nil, false
	}
	return &TextDoc{Text: units, IDs: ids}, marks, true
}

// SaveText is doc and marks as stored (and as a base).
func SaveText(doc *TextDoc, marks TextMarks) *delta.JsonObject {
	saved := doc.IDs.SaveCompact()
	numbers := func(values []int64) []any {
		out := make([]any, len(values))
		for i, value := range values {
			out[i] = float64(value)
		}
		return out
	}
	bunches := make([]any, len(saved.BunchIDs))
	for i, bunch := range saved.BunchIDs {
		bunches[i] = bunch
	}
	stored := delta.NewJsonObject(len(marks))
	for editor, mark := range marks {
		stored.Set(editor, float64(mark))
	}
	return delta.JsonObjectOf("text", doc.String(), "textIds", delta.JsonObjectOf(
		"bunchIds", bunches, "signedCounts", numbers(saved.SignedCounts),
		"startCounterDeltas", numbers(saved.StartCounterDeltas), "startCounterValues", numbers(saved.StartCounterValues),
	), "textMarks", stored)
}

// known is id as an element ID of the list, when it is a counter the list can know.
func (id TextID) known() (ElementID, bool) {
	if !isSafeInteger(id.Counter) || id.Counter < 0 {
		return ElementID{}, false
	}
	return ElementID{BunchID: id.BunchID, Counter: int64(id.Counter)}, true
}

// ApplyEdit is doc after edit, or doc itself when the rules drop it (unknown or taken IDs, a stale range, the limit).
func ApplyEdit(doc *TextDoc, edit TextEdit) *TextDoc {
	if edit.Insert {
		return applyInsert(doc, edit)
	}
	return applyDelete(doc, edit)
}

func applyInsert(doc *TextDoc, edit TextEdit) *TextDoc {
	content := utf16.Encode([]rune(edit.Content))
	if len(content) == 0 || len(doc.Text)+len(content) > MaxText {
		return doc
	}
	// The whole range of IDs the insert takes must be safe integers; articulated refuses one that overlaps a known ID.
	if !isSafeInteger(edit.ID.Counter) || edit.ID.Counter < 0 || !isSafeInteger(edit.ID.Counter+float64(len(content))) {
		return doc
	}
	var before *ElementID
	if edit.Before != nil {
		id, valid := edit.Before.known()
		if !valid || !doc.IDs.IsKnown(id) {
			return doc
		}
		if edit.InWord && !doc.IDs.Has(id) {
			return doc
		}
		before = &id
	}
	ids, err := doc.IDs.InsertAfter(before, ElementID{BunchID: edit.ID.BunchID, Counter: int64(edit.ID.Counter)}, int64(len(content)))
	if err != nil {
		return doc
	}
	at := 0
	if before != nil {
		index, _ := doc.IDs.IndexOf(*before, BiasLeft)
		at = index + 1
	}
	text := make([]uint16, 0, len(doc.Text)+len(content))
	text = append(append(append(text, doc.Text[:at]...), content...), doc.Text[at:]...)
	return &TextDoc{Text: text, IDs: ids}
}

func applyDelete(doc *TextDoc, edit TextEdit) *TextDoc {
	start, startValid := edit.Start.known()
	end, endValid := edit.End.known()
	if !startValid || !endValid || !doc.IDs.IsKnown(start) || !doc.IDs.IsKnown(end) {
		return doc
	}
	from, _ := doc.IDs.IndexOf(start, BiasRight)
	to, _ := doc.IDs.IndexOf(end, BiasLeft)
	if to < from || float64(to-from+1) > edit.Length+growth {
		return doc
	}
	ids, err := doc.IDs.DeleteRange(from, to+1)
	if err != nil {
		return doc
	}
	text := make([]uint16, 0, len(doc.Text))
	text = append(append(text, doc.Text[:from]...), doc.Text[to+1:]...)
	return &TextDoc{Text: text, IDs: ids}
}

// ApplyEdits applies edits in order.
func ApplyEdits(doc *TextDoc, edits []TextEdit) *TextDoc {
	next := doc
	for _, edit := range edits {
		next = ApplyEdit(next, edit)
	}
	return next
}

func isWordUnit(unit uint16) bool {
	if unit >= 0xD800 && unit <= 0xDFFF {
		return false
	}
	character := rune(unit)
	return unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_'
}

// EditsTo is the edits that turn doc's text into next: a delete of what changed and an insert of what replaces it (the common
// start and end kept), with new IDs from newID(before, count).
func EditsTo(doc *TextDoc, next string, newID func(before *ElementID, count int64) ElementID) []TextEdit {
	old := doc.Text
	wanted := utf16.Encode([]rune(next))
	prefix := 0
	shorter := min(len(old), len(wanted))
	for prefix < shorter && old[prefix] == wanted[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < shorter-prefix && old[len(old)-1-suffix] == wanted[len(wanted)-1-suffix] {
		suffix++
	}
	edits := []TextEdit{}
	removed := len(old) - suffix - prefix
	asText := func(id ElementID) TextID { return TextID{BunchID: id.BunchID, Counter: float64(id.Counter)} }
	if removed > 0 {
		start, _ := doc.IDs.At(prefix)
		end, _ := doc.IDs.At(prefix + removed - 1)
		edits = append(edits, TextEdit{Start: asText(start), End: asText(end), Length: float64(removed)})
	}
	content := wanted[prefix : len(wanted)-suffix]
	if len(content) > 0 {
		var before *ElementID
		var beforeText *TextID
		if prefix > 0 {
			id, _ := doc.IDs.At(prefix - 1)
			before = &id
			text := asText(id)
			beforeText = &text
		}
		edits = append(edits, TextEdit{
			Insert: true, Before: beforeText, ID: asText(newID(before, int64(len(content)))), Content: string(utf16.Decode(content)),
			InWord: prefix > 0 && isWordUnit(old[prefix-1]) && isWordUnit(content[0]),
		})
	}
	return edits
}

// editOf is an edit from its wire form (the contract's schema checked it).
func editOf(value any) (TextEdit, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return TextEdit{}, errors.New("an edit is an object")
	}
	idOf := func(value any) TextID {
		id, _ := value.(*delta.JsonObject)
		bunch, _ := id.Value("bunchId").(string)
		counter, _ := id.Value("counter").(float64)
		return TextID{BunchID: bunch, Counter: counter}
	}
	if object.Value("kind") == "insert" {
		edit := TextEdit{Insert: true, ID: idOf(object.Value("id"))}
		if object.Value("before") != nil {
			before := idOf(object.Value("before"))
			edit.Before = &before
		}
		edit.Content, _ = object.Value("content").(string)
		edit.InWord, _ = object.Value("inWord").(bool)
		return edit, nil
	}
	length, _ := object.Value("length").(float64)
	return TextEdit{Start: idOf(object.Value("start")), End: idOf(object.Value("end")), Length: length}, nil
}

// editsOf is a batch of edits from its wire form.
func editsOf(value any) ([]TextEdit, error) {
	list, _ := value.([]any)
	edits := make([]TextEdit, len(list))
	for i, each := range list {
		edit, err := editOf(each)
		if err != nil {
			return nil, err
		}
		edits[i] = edit
	}
	return edits, nil
}

type textIDJSON struct {
	BunchID string  `json:"bunchId"`
	Counter float64 `json:"counter"`
}

type editJSON struct {
	Kind    string       `json:"kind"`
	Before  **textIDJSON `json:"before,omitempty"`
	ID      *textIDJSON  `json:"id,omitempty"`
	Content *string      `json:"content,omitempty"`
	InWord  *bool        `json:"inWord,omitempty"`
	Start   *textIDJSON  `json:"start,omitempty"`
	End     *textIDJSON  `json:"end,omitempty"`
	Length  *float64     `json:"length,omitempty"`
}

// EditsJSON is `JSON.stringify(edits)`: a logged batch's data.
func EditsJSON(edits []TextEdit) string {
	list := make([]editJSON, len(edits))
	for i, edit := range edits {
		if edit.Insert {
			id := textIDJSON(edit.ID)
			var before *textIDJSON
			if edit.Before != nil {
				value := textIDJSON(*edit.Before)
				before = &value
			}
			content, inWord := edit.Content, edit.InWord
			list[i] = editJSON{Kind: "insert", Before: &before, ID: &id, Content: &content, InWord: &inWord}
			continue
		}
		start, end, length := textIDJSON(edit.Start), textIDJSON(edit.End), edit.Length
		list[i] = editJSON{Kind: "delete", Start: &start, End: &end, Length: &length}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(list); err != nil {
		return "[]"
	}
	return string(bytes.TrimSuffix(out.Bytes(), []byte("\n")))
}

// parseEdits is a logged batch's edits (the server wrote them: they parse).
func parseEdits(data string) []TextEdit {
	var list []struct {
		Kind    string      `json:"kind"`
		Before  *textIDJSON `json:"before"`
		ID      textIDJSON  `json:"id"`
		Content string      `json:"content"`
		InWord  bool        `json:"inWord"`
		Start   textIDJSON  `json:"start"`
		End     textIDJSON  `json:"end"`
		Length  float64     `json:"length"`
	}
	if json.Unmarshal([]byte(data), &list) != nil {
		return nil
	}
	edits := make([]TextEdit, len(list))
	for i, each := range list {
		if each.Kind == "insert" {
			edit := TextEdit{Insert: true, ID: TextID(each.ID), Content: each.Content, InWord: each.InWord}
			if each.Before != nil {
				before := TextID(*each.Before)
				edit.Before = &before
			}
			edits[i] = edit
			continue
		}
		edits[i] = TextEdit{Start: TextID(each.Start), End: TextID(each.End), Length: each.Length}
	}
	return edits
}
