package kitwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// representativeView uses every node kind and field the host's ViewPayload
// carries (coding/extension/host/subprocess/protocol.go).
func representativeView(image []byte) kit.View {
	text := kit.NewText("Kit probe", 2, 0)
	text.Bg = "customMessageBg"
	box := kit.NewBox(1, 1, kit.NewTruncatedText("boxed", 0, 0))
	box.Bg = "selectedBg"
	markdown := kit.NewMarkdown("- one\n- **two**", 1, 0)
	markdown.DefaultTextStyle = &kit.TextStyle{Color: "text", BgColor: "customMessageBg", Bold: true, Italic: true, Strikethrough: true, Underline: true}
	markdown.RenderLatex = new(false)
	list := kit.NewSelectList("tracks", []kit.SelectItem{{Value: "k0", Label: "Track 0", Description: "Artist 0"}, {Value: "k1", Label: "Track 1"}}, 3)
	list.Layout = kit.SelectListLayout{MinPrimaryColumnWidth: new(10), MaxPrimaryColumnWidth: new(20)}
	list.SetSelectedIndex(1)
	list.SetFilter("tr")
	settings := kit.NewSettingsList("settings", []kit.SettingItem{
		{ID: "theme", Label: "Theme", Description: "Color theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
		{ID: "model", Label: "Model", CurrentValue: "a", Submenu: kit.NewText("pick", 0, 0)},
	}, 5)
	settings.EnableSearch = true
	settings.SetSelectedIndex(0)
	settings.SetFilter("th")
	img := kit.NewImage(image, "image/png")
	img.MaxWidthCells = new(20)
	img.MaxHeightCells = new(10)
	img.Filename = "cover.png"
	img.FallbackColor = "muted"
	loader := kit.NewLoader("Working")
	loader.SpinnerColor = "accent"
	loader.MessageColor = "muted"
	loader.Indicator = &kit.LoaderIndicator{Frames: []string{"a", "b"}, IntervalMs: 120}
	loader.Frame = new(1)
	hidden := kit.NewLoader("Hidden")
	hidden.Indicator = &kit.LoaderIndicator{Frames: []string{}}
	hstack := kit.NewHStack(
		kit.Entry(kit.NewTruncatedText("left side", 0, 0), kit.StackEntryOptions{Grow: new(1)}),
		kit.Entry(kit.NewTruncatedText("right", 0, 0), kit.StackEntryOptions{Basis: new(5), Shrink: new(0), MinSize: new(2), MaxSize: new(9)}),
		kit.NewSpacer(1),
	)
	hstack.Gap = 1
	hstack.Align = "center"
	lines := kit.NewLines([]string{"row 1", "row 2"})
	lines.Progress = &kit.Progress{Value: 30, Max: 120}
	lines.Image = &kit.LinesImage{Data: image, MimeType: "image/png"}
	lines.List = &kit.List{Items: []kit.ListItem{{Label: "Blue in Green", Detail: "Miles Davis", Columns: []string{"Kind of Blue", "5:37"}}, {Label: "So What"}}, Selected: 0}
	return kit.View{
		Root: kit.NewContainer(
			kit.NewDynamicBorder("accent"),
			kit.NewDynamicBorder(""),
			text,
			box,
			markdown,
			kit.NewSpacer(2),
			list,
			settings,
			img,
			loader,
			hidden,
			hstack,
			kit.NewVStack(kit.NewText("", 1, 1)),
			lines,
		),
		Focus: "tracks",
		Theme: map[string]string{"accent": "#d75f00", "muted": "#808080"},
	}
}

func TestEncodeMatchesTheHostViewPayload(t *testing.T) {
	image := []byte("png bytes")
	sum := sha256.Sum256(image)
	ref := hex.EncodeToString(sum[:])
	encoded, err := Encode(representativeView(image), true)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"root":{"kind":"container","children":[` +
		`{"kind":"dynamic-border","color":"accent"},` +
		`{"kind":"dynamic-border"},` +
		`{"kind":"text","text":"Kit probe","paddingX":2,"paddingY":0,"bg":"customMessageBg"},` +
		`{"kind":"box","children":[{"kind":"truncated-text","text":"boxed","paddingX":0,"paddingY":0}],"paddingX":1,"paddingY":1,"bg":"selectedBg"},` +
		`{"kind":"markdown","text":"- one\n- **two**","paddingX":1,"paddingY":0,"defaultTextStyle":{"color":"text","bgColor":"customMessageBg","bold":true,"italic":true,"strikethrough":true,"underline":true},"renderLatex":false},` +
		`{"kind":"spacer","lines":2},` +
		`{"kind":"select-list","id":"tracks","items":[{"value":"k0","label":"Track 0","description":"Artist 0"},{"value":"k1","label":"Track 1"}],"maxVisible":3,"layout":{"minPrimaryColumnWidth":10,"maxPrimaryColumnWidth":20},"selectedIndex":1,"filter":"tr"},` +
		`{"kind":"settings-list","id":"settings","items":[{"id":"theme","label":"Theme","description":"Color theme","currentValue":"dark","values":["dark","light"]},{"id":"model","label":"Model","currentValue":"a","submenu":{"kind":"text","text":"pick","paddingX":0,"paddingY":0}}],"maxVisible":5,"selectedIndex":0,"filter":"th","enableSearch":true},` +
		`{"kind":"image","ref":"` + ref + `","mimeType":"image/png","maxWidthCells":20,"maxHeightCells":10,"filename":"cover.png","fallbackColor":"muted"},` +
		`{"kind":"loader","message":"Working","spinnerColor":"accent","messageColor":"muted","indicator":{"frames":["a","b"],"intervalMs":120},"frame":1},` +
		`{"kind":"loader","message":"Hidden","indicator":{"frames":[]}},` +
		`{"kind":"hstack","children":[` +
		`{"kind":"truncated-text","stack":{"grow":1},"text":"left side","paddingX":0,"paddingY":0},` +
		`{"kind":"truncated-text","stack":{"basis":5,"shrink":0,"minSize":2,"maxSize":9},"text":"right","paddingX":0,"paddingY":0},` +
		`{"kind":"spacer","lines":1}],"gap":1,"align":"center"},` +
		`{"kind":"vstack","children":[{"kind":"text","paddingX":1,"paddingY":1}]},` +
		`{"kind":"lines","content":["row 1","row 2"],"image":{"ref":"` + ref + `"},"progress":{"value":30,"max":120},"list":{"items":[{"label":"Blue in Green","detail":"Miles Davis","columns":["Kind of Blue","5:37"]},{"label":"So What"}],"selectedIndex":0}}` +
		`]},"focus":"tracks","theme":{"accent":"#d75f00","muted":"#808080"}}`
	if string(encoded.View) != want {
		t.Fatalf("view JSON\n got %s\nwant %s", encoded.View, want)
	}
	if len(encoded.Images) != 1 || encoded.Images[0].Ref != ref || encoded.Images[0].MimeType != "image/png" || !bytes.Equal(encoded.Images[0].Data, image) {
		t.Fatalf("images = %+v, want the one image once", encoded.Images)
	}
	payload, err := encoded.Payload(encoded.Images)
	if err != nil {
		t.Fatal(err)
	}
	wantPayload := want[:len(want)-1] + `,"images":[{"ref":"` + ref + `","mimeType":"image/png","data":"cG5nIGJ5dGVz"}]}`
	if string(payload) != wantPayload {
		t.Fatalf("payload\n got %s\nwant %s", payload, wantPayload)
	}
}

func TestEncodeOmitsFrontendOnlyAnnotationsWithoutAFrontend(t *testing.T) {
	data := []byte("cover")
	lines := kit.NewLines([]string{"▀▀"})
	lines.Image = &kit.LinesImage{Data: data, MimeType: "image/jpeg"}
	lines.Progress = &kit.Progress{Value: 1, Max: 2}
	lines.List = &kit.List{Items: []kit.ListItem{{Label: "cover"}}, Selected: -1}
	view := kit.View{Root: lines}

	without, err := Encode(view, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(without.View), `{"root":{"kind":"lines","content":["▀▀"]}}`; got != want {
		t.Fatalf("without frontend = %s, want %s", got, want)
	}
	if len(without.Images) != 0 {
		t.Fatalf("without frontend images = %+v, want none", without.Images)
	}

	with, err := Encode(view, true)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:])
	if got, want := string(with.View), `{"root":{"kind":"lines","content":["▀▀"],"image":{"ref":"`+ref+`"},"progress":{"value":1,"max":2},"list":{"items":[{"label":"cover"}],"selectedIndex":-1}}}`; got != want {
		t.Fatalf("with frontend = %s, want %s", got, want)
	}
	if len(with.Images) != 1 || with.Images[0].Ref != ref {
		t.Fatalf("with frontend images = %+v", with.Images)
	}
}

func TestEncodeRejectsMissingNodesAndCycles(t *testing.T) {
	if _, err := Encode(kit.View{}, false); err == nil {
		t.Fatal("a view without a root encoded")
	}
	if _, err := Encode(kit.View{Root: kit.NewContainer(nil)}, false); err == nil {
		t.Fatal("a nil child encoded")
	}
	var text *kit.Text
	if _, err := Encode(kit.View{Root: kit.NewHStack(text)}, false); err == nil {
		t.Fatal("a typed nil stack child encoded")
	}
	cycle := kit.NewContainer()
	cycle.AddChild(cycle)
	if _, err := Encode(kit.View{Root: cycle}, false); err == nil {
		t.Fatal("a cyclic tree encoded")
	}
}

// The conversation kinds (docs/plan/extension-component-kit.md §2.1) encode
// their constructor arguments always, their options only off upstream's
// defaults, a tool result's isPartial always, and a result image once as a
// kit image.
func TestEncodeConversationKinds(t *testing.T) {
	image := []byte("png bytes")
	sum := sha256.Sum256(image)
	ref := hex.EncodeToString(sum[:])
	assistant := kit.NewAssistantMessage(nil)
	assistant.ID = "a1"
	assistant.UpdateContent(kit.Message{Content: []kit.ContentBlock{kit.ThinkingBlock("plan"), kit.TextBlock("done"), kit.ToolCallBlock()}, StopReason: "toolUse"}, true)
	hidden := kit.NewAssistantMessage(&kit.Message{Content: []kit.ContentBlock{kit.TextBlock("x")}, StopReason: "error", ErrorMessage: "boom"})
	hidden.SetHideThinkingBlock(true)
	hidden.SetHiddenThinkingLabel("Pondering...")
	hidden.SetOutputPad(0)
	tool := kit.NewToolExecution("read", "call-1", map[string]any{"path": "a.txt"}, "/work")
	tool.ID = "call-1"
	tool.SetArgsComplete()
	tool.MarkExecutionStarted()
	tool.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("hi"), kit.ImageContent(image, "image/png")}, IsError: true, Details: map[string]any{"n": 1}}, false)
	tool.SetExpanded(true)
	empty := kit.NewToolExecution("kit_tool", "", nil, "")
	empty.ToolDefinition = kit.ToolDefinitionEmpty
	empty.SetShowImages(false)
	empty.SetImageWidthCells(30)
	bash := kit.NewBashExecution("ls", true)
	bash.ID = "b"
	bash.AppendOutput("a\n")
	bash.AppendOutput("b")
	bash.SetComplete(new(2), false, true, "/tmp/out")
	bash.SetExpanded(true)
	diff := kit.NewDiff(" 1 a\n-2 b\n+2 c")
	diff.FilePath = "x.go"
	view := kit.View{Root: kit.NewContainer(kit.NewUserMessage("hello"), assistant, hidden, tool, empty, bash, kit.NewBashExecution("sleep", false), diff)}
	encoded, err := Encode(view, false)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"root":{"kind":"container","children":[` +
		`{"kind":"user-message","text":"hello","outputPad":1},` +
		`{"kind":"assistant-message","id":"a1","message":{"content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"done"},{"type":"toolCall"}],"stopReason":"toolUse"},"outputPad":1,"isStreaming":true},` +
		`{"kind":"assistant-message","message":{"content":[{"type":"text","text":"x"}],"stopReason":"error","errorMessage":"boom"},"outputPad":0,"hideThinkingBlock":true,"hiddenThinkingLabel":"Pondering..."},` +
		`{"kind":"tool-execution","id":"call-1","toolName":"read","toolCallId":"call-1","args":{"path":"a.txt"},"cwd":"/work","executionStarted":true,"argsComplete":true,"expanded":true,"result":{"content":[{"type":"text","text":"hi"},{"type":"image","ref":"` + ref + `","mimeType":"image/png"}],"isError":true,"details":{"n":1}},"isPartial":false},` +
		`{"kind":"tool-execution","toolName":"kit_tool","args":{},"toolDefinition":"empty","showImages":false,"imageWidthCells":30},` +
		`{"kind":"bash-execution","id":"b","expanded":true,"command":"ls","excludeFromContext":true,"output":"a\nb","complete":{"exitCode":2,"truncated":true,"fullOutputPath":"/tmp/out"}},` +
		`{"kind":"bash-execution","command":"sleep"},` +
		`{"kind":"diff","paddingX":0,"paddingY":0,"diff":" 1 a\n-2 b\n+2 c","filePath":"x.go"}]}}`
	if string(encoded.View) != want {
		t.Fatalf("view JSON\n got %s\nwant %s", encoded.View, want)
	}
	if len(encoded.Images) != 1 || encoded.Images[0].Ref != ref {
		t.Fatalf("images = %+v, want the result image once", encoded.Images)
	}
}
