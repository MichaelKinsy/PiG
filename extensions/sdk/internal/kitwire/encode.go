// Package kitwire encodes a kit.View as the host's ViewPayload JSON
// (coding/extension/host/subprocess/protocol.go). Field names and omission
// rules match the host's ViewPayload and ViewNode; constructor arguments
// (paddings, a spacer's lines, maxVisible, a loader's message) are always
// explicit, and other fields are omitted when they hold upstream's default.
// The SDK validates nothing beyond what encoding needs: the host is the only
// validator of tokens, colors, ids and bounds.
package kitwire

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// maxDepth is the host's depth bound. Encoding stops there, so a tree that
// contains itself fails instead of recursing without end.
const maxDepth = 64

// Image is image bytes a view references.
type Image struct {
	Ref      string
	MimeType string
	Data     []byte
}

// Encoded is an encoded view.
type Encoded struct {
	// View is the ViewPayload JSON without images; equal views encode to
	// equal bytes.
	View []byte
	// Images are the images the view references, each once, in the order the
	// tree first references them.
	Images []Image
}

// References reports whether the view references any ref in refs.
func (e Encoded) References(refs map[string]struct{}) bool {
	for _, image := range e.Images {
		if _, ok := refs[image.Ref]; ok {
			return true
		}
	}
	return false
}

// Payload returns the ViewPayload JSON carrying the bytes of images, which
// are the view's images the connection has not sent yet.
func (e Encoded) Payload(images []Image) (json.RawMessage, error) {
	if len(images) == 0 {
		return e.View, nil
	}
	data := make([]imageData, len(images))
	for i, image := range images {
		data[i] = imageData(image)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	// View is an object that ends with its closing brace and holds root, so
	// images follow as its last member.
	payload := make([]byte, 0, len(e.View)+len(`,"images":`)+len(encoded))
	payload = append(payload, e.View[:len(e.View)-1]...)
	payload = append(payload, `,"images":`...)
	payload = append(payload, encoded...)
	payload = append(payload, '}')
	return payload, nil
}

// Encode encodes view. Frontend-only annotations of lines nodes, and the
// bytes of their images, are encoded only when frontend is set.
func Encode(view kit.View, frontend bool) (Encoded, error) {
	enc := encoder{frontend: frontend}
	if view.Root == nil {
		return Encoded{}, errors.New("kit: view has no root")
	}
	root, err := enc.node(view.Root, 0)
	if err != nil {
		return Encoded{}, err
	}
	data, err := json.Marshal(viewPayload{Root: root, Focus: view.Focus, Theme: view.Theme})
	if err != nil {
		return Encoded{}, err
	}
	return Encoded{View: data, Images: enc.images}, nil
}

type encoder struct {
	frontend bool
	images   []Image
}

func (e *encoder) addImage(ref, mimeType string, data []byte) {
	for _, image := range e.images {
		if image.Ref == ref {
			return
		}
	}
	e.images = append(e.images, Image{Ref: ref, MimeType: mimeType, Data: data})
}

func (e *encoder) nodes(children []kit.Node, depth int) ([]viewNode, error) {
	if len(children) == 0 {
		return nil, nil
	}
	out := make([]viewNode, len(children))
	for i, child := range children {
		node, err := e.node(child, depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = node
	}
	return out, nil
}

func (e *encoder) stackChildren(children []kit.StackChild, depth int) ([]viewNode, error) {
	if len(children) == 0 {
		return nil, nil
	}
	out := make([]viewNode, len(children))
	for i, child := range children {
		var node viewNode
		var err error
		switch child := child.(type) {
		case kit.StackEntry:
			node, err = e.node(child.Node, depth+1)
			if err == nil {
				node.Stack = stackEntry(child.Options)
			}
		case kit.Node:
			node, err = e.node(child, depth+1)
		default:
			err = errors.New("kit: nil stack child")
		}
		if err != nil {
			return nil, err
		}
		out[i] = node
	}
	return out, nil
}

func stackEntry(options kit.StackEntryOptions) *viewStackEntry {
	if options == (kit.StackEntryOptions{}) {
		return nil
	}
	return &viewStackEntry{Basis: options.Basis, Grow: options.Grow, Shrink: options.Shrink, MinSize: options.MinSize, MaxSize: options.MaxSize}
}

func (e *encoder) node(node kit.Node, depth int) (viewNode, error) {
	if depth >= maxDepth {
		return viewNode{}, fmt.Errorf("kit: view deeper than %d nodes", maxDepth)
	}
	var out viewNode
	var err error
	switch n := node.(type) {
	case *kit.Container:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindContainer}
		out.Children, err = e.nodes(n.Children, depth)
		return out, err
	case *kit.Box:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindBox, PaddingX: new(n.PaddingX), PaddingY: new(n.PaddingY), Bg: n.Bg}
		out.Children, err = e.nodes(n.Children, depth)
		return out, err
	case *kit.Text:
		if n == nil {
			break
		}
		return viewNode{Kind: kindText, Text: n.Text, PaddingX: new(n.PaddingX), PaddingY: new(n.PaddingY), Bg: n.Bg}, nil
	case *kit.TruncatedText:
		if n == nil {
			break
		}
		return viewNode{Kind: kindTruncatedText, Text: n.Text, PaddingX: new(n.PaddingX), PaddingY: new(n.PaddingY)}, nil
	case *kit.Markdown:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindMarkdown, Text: n.Text, PaddingX: new(n.PaddingX), PaddingY: new(n.PaddingY), RenderLatex: n.RenderLatex}
		if style := n.DefaultTextStyle; style != nil {
			out.DefaultTextStyle = &viewTextStyle{Color: style.Color, BgColor: style.BgColor, Bold: style.Bold, Italic: style.Italic, Strikethrough: style.Strikethrough, Underline: style.Underline}
		}
		return out, nil
	case *kit.Spacer:
		if n == nil {
			break
		}
		return viewNode{Kind: kindSpacer, Lines: new(n.Lines)}, nil
	case *kit.DynamicBorder:
		if n == nil {
			break
		}
		return viewNode{Kind: kindDynamicBorder, Color: n.Color}, nil
	case *kit.SelectList:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindSelectList, ID: n.ID, MaxVisible: new(n.MaxVisible), SelectedIndex: n.SelectedIndex, Filter: n.Filter}
		if len(n.Items) > 0 {
			out.Items = make([]viewItem, len(n.Items))
			for i, item := range n.Items {
				out.Items[i] = viewItem{Value: item.Value, Label: item.Label, Description: item.Description}
			}
		}
		if n.Layout.MinPrimaryColumnWidth != nil || n.Layout.MaxPrimaryColumnWidth != nil {
			out.Layout = &viewSelectLayout{MinPrimaryColumnWidth: n.Layout.MinPrimaryColumnWidth, MaxPrimaryColumnWidth: n.Layout.MaxPrimaryColumnWidth}
		}
		return out, nil
	case *kit.SettingsList:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindSettingsList, ID: n.ID, MaxVisible: new(n.MaxVisible), EnableSearch: n.EnableSearch, SelectedIndex: n.SelectedIndex, Filter: n.Filter}
		if len(n.Items) > 0 {
			out.Items = make([]viewItem, len(n.Items))
			for i, item := range n.Items {
				out.Items[i] = viewItem{ID: item.ID, Label: item.Label, Description: item.Description, CurrentValue: item.CurrentValue, Values: item.Values}
				if item.Submenu != nil {
					submenu, err := e.node(item.Submenu, depth+1)
					if err != nil {
						return viewNode{}, err
					}
					out.Items[i].Submenu = &submenu
				}
			}
		}
		return out, nil
	case *kit.Image:
		if n == nil {
			break
		}
		ref := n.Ref()
		e.addImage(ref, n.MimeType, n.Data())
		return viewNode{Kind: kindImage, Ref: ref, MimeType: n.MimeType, MaxWidthCells: n.MaxWidthCells, MaxHeightCells: n.MaxHeightCells, Filename: n.Filename, FallbackColor: n.FallbackColor}, nil
	case *kit.Loader:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindLoader, Message: n.Message, SpinnerColor: n.SpinnerColor, MessageColor: n.MessageColor, Frame: n.Frame}
		if indicator := n.Indicator; indicator != nil {
			out.Indicator = &viewLoaderIndicator{IntervalMs: indicator.IntervalMs}
			if indicator.Frames != nil {
				out.Indicator.Frames = &indicator.Frames
			}
		}
		return out, nil
	case *kit.HStack:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindHStack, Gap: gap(n.Gap), Align: n.Align}
		out.Children, err = e.stackChildren(n.Children, depth)
		return out, err
	case *kit.VStack:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindVStack, Gap: gap(n.Gap), Align: n.Align}
		out.Children, err = e.stackChildren(n.Children, depth)
		return out, err
	case *kit.Lines:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindLines, Content: n.Lines}
		if e.frontend {
			if image := n.Image; image != nil {
				ref := imageRef(image.Data)
				e.addImage(ref, image.MimeType, image.Data)
				out.Image = &viewImageRef{Ref: ref}
			}
			if progress := n.Progress; progress != nil {
				out.Progress = &viewProgress{Value: progress.Value, Max: progress.Max}
			}
			if list := n.List; list != nil {
				items := make([]viewListItem, len(list.Items))
				for i, item := range list.Items {
					items[i] = viewListItem(item)
				}
				out.List = &viewList{Items: items, SelectedIndex: list.Selected}
			}
		}
		return out, nil
	case *kit.UserMessage:
		if n == nil {
			break
		}
		return viewNode{Kind: kindUserMessage, Text: n.Text, OutputPad: new(n.OutputPad)}, nil
	case *kit.AssistantMessage:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindAssistantMessage, ID: n.ID, HideThinkingBlock: n.HideThinkingBlock, OutputPad: new(n.OutputPad), IsStreaming: n.IsStreaming}
		if n.HiddenThinkingLabel != "Thinking..." {
			out.HiddenThinkingLabel = new(n.HiddenThinkingLabel)
		}
		if m := n.Message; m != nil {
			message := &viewAssistantMessage{Content: make([]viewContentBlock, len(m.Content)), StopReason: m.StopReason, ErrorMessage: m.ErrorMessage}
			for i, block := range m.Content {
				message.Content[i] = viewContentBlock{Type: block.Type, Text: block.Text, Thinking: block.Thinking}
			}
			out.Message = message
		}
		return out, nil
	case *kit.ToolExecution:
		if n == nil {
			break
		}
		return e.toolExecution(n)
	case *kit.BashExecution:
		if n == nil {
			break
		}
		out = viewNode{Kind: kindBashExecution, ID: n.ID, Command: n.Command, ExcludeFromContext: n.ExcludeFromContext, Output: n.Output, Expanded: n.Expanded}
		if c := n.Complete; c != nil {
			out.Complete = &viewBashComplete{ExitCode: c.ExitCode, Cancelled: c.Cancelled, Truncated: c.Truncated, FullOutputPath: c.FullOutputPath}
		}
		return out, nil
	case *kit.Diff:
		if n == nil {
			break
		}
		return viewNode{Kind: kindDiff, Diff: n.Diff, FilePath: n.FilePath, PaddingX: new(n.PaddingX), PaddingY: new(n.PaddingY)}, nil
	}
	return viewNode{}, errors.New("kit: nil node")
}

// toolExecution encodes a tool card: its constructor arguments always, its
// options when they differ from upstream's defaults, and isPartial with a
// result.
func (e *encoder) toolExecution(n *kit.ToolExecution) (viewNode, error) {
	out := viewNode{Kind: kindToolExecution, ID: n.ID, ToolName: n.ToolName, ToolCallID: n.ToolCallID, Cwd: n.Cwd,
		ExecutionStarted: n.ExecutionStarted, ArgsComplete: n.ArgsComplete, Expanded: n.Expanded}
	args, err := jsonValue(n.Args, `{}`)
	if err != nil {
		return viewNode{}, fmt.Errorf("kit: tool %s arguments: %w", n.ToolName, err)
	}
	out.Args = args
	if n.ToolDefinition != kit.ToolDefinitionBuiltin {
		out.ToolDefinition = string(n.ToolDefinition)
	}
	if !n.ShowImages {
		out.ShowImages = new(false)
	}
	if n.ImageWidthCells != 60 {
		out.ImageWidthCells = new(n.ImageWidthCells)
	}
	if r := n.Result; r != nil {
		result := &viewToolResult{Content: make([]viewContentBlock, len(r.Content)), IsError: r.IsError}
		for i, block := range r.Content {
			if image := block.Image; image != nil {
				ref := image.Ref()
				e.addImage(ref, image.MimeType, image.Data())
				result.Content[i] = viewContentBlock{Type: "image", Ref: ref, MimeType: image.MimeType}
				continue
			}
			result.Content[i] = viewContentBlock{Type: "text", Text: block.Text}
		}
		if r.Details != nil {
			if result.Details, err = jsonValue(r.Details, ""); err != nil {
				return viewNode{}, fmt.Errorf("kit: tool %s result details: %w", n.ToolName, err)
			}
		}
		out.Result = result
		out.IsPartial = new(n.IsPartial)
	}
	return out, nil
}

// jsonValue encodes value as JSON, and nil as empty.
func jsonValue(value any, empty string) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage(empty), nil
	}
	if raw, ok := value.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(value)
}

// gap omits upstream's default gap (0).
func gap(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

// imageRef is the lowercase hex SHA-256 of data, as kit.Image.Ref.
func imageRef(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
