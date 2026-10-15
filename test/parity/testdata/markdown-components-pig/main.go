package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/tui"
)

type sample struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Width           int    `json:"width"`
	PaddingX        int    `json:"paddingX"`
	PaddingY        int    `json:"paddingY"`
	PreserveMarkers bool   `json:"preserveMarkers"`
	PreserveEscapes bool   `json:"preserveEscapes"`
	RenderLatex     *bool  `json:"renderLatex"`
	Color           string `json:"color"`
	Italic          bool   `json:"italic"`
	Links           bool   `json:"links"`
	QuoteRGB        bool   `json:"quoteRGB"`
}

func chalk(open, close string) func(string) string {
	lineBreaks := strings.NewReplacer("\r\n", close+"\r\n"+open, "\n", close+"\n"+open)
	return func(s string) string {
		if s == "" {
			return ""
		}
		return open + lineBreaks.Replace(strings.ReplaceAll(s, close, close+open)) + close
	}
}
func fg(code string) func(string) string { return chalk("\x1b["+code+"m", "\x1b[39m") }
func theme() *tui.MarkdownTheme {
	bold := chalk("\x1b[1m", "\x1b[22m")
	italic := chalk("\x1b[3m", "\x1b[23m")
	dim := chalk("\x1b[2m", "\x1b[22m")
	return &tui.MarkdownTheme{Heading: func(s string) string { return bold(fg("36")(s)) }, Link: fg("34"), LinkUrl: dim, Code: fg("33"), CodeBlock: fg("32"), CodeBlockBorder: dim, Quote: italic, QuoteBorder: dim, Hr: dim, ListBullet: fg("36"), Bold: bold, Italic: italic, Strikethrough: chalk("\x1b[9m", "\x1b[29m"), Underline: chalk("\x1b[4m", "\x1b[24m")}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	file, err := os.Open("test/parity/testdata/markdown-component-corpus.json")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var cases []sample
	if err := decoder.Decode(&cases); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	emit := func(name string, lines []string) error {
		return enc.Encode(struct {
			Name  string   `json:"name"`
			Lines []string `json:"lines"`
		}{name, lines})
	}
	for _, s := range cases {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: s.Links})
		th := theme()
		if s.QuoteRGB {
			th.Quote = func(s string) string { return "\x1b[38;2;18;52;86m" + s + "\x1b[39m" }
			th.Link = func(s string) string { return "\x1b[38;2;129;162;190m" + s + "\x1b[39m" }
		}
		var style *tui.DefaultTextStyle
		if s.Color != "" || s.Italic {
			style = &tui.DefaultTextStyle{Italic: s.Italic}
			if s.Color == "gray" {
				style.Color = fg("90")
			} else if s.Color != "" {
				return fmt.Errorf("unknown color %q", s.Color)
			}
		}
		md := tui.NewMarkdownWithOptions(s.Source, s.PaddingX, s.PaddingY, th, style, &tui.MarkdownOptions{PreserveOrderedListMarkers: s.PreserveMarkers, PreserveBackslashEscapes: s.PreserveEscapes, RenderLatex: s.RenderLatex})
		if err := emit(s.Name, md.Render(s.Width)); err != nil {
			return err
		}
	}
	type call struct {
		Source string `json:"source"`
		Width  int    `json:"availableWidth"`
	}
	var calls []call
	var frames [][]string
	md := tui.NewMarkdownWithOptions("source", 2, 0, theme(), nil, &tui.MarkdownOptions{Transform: func(s string, w int) string { calls = append(calls, call{s, w}); return s + " " + strconv.Itoa(w) }})
	frames = append(frames, md.Render(80))
	md.Render(80)
	frames = append(frames, md.Render(60))
	md.SetText("updated")
	frames = append(frames, md.Render(60))
	md.Invalidate()
	frames = append(frames, md.Render(60))
	if err := enc.Encode(struct {
		Name   string     `json:"name"`
		Calls  []call     `json:"calls"`
		Frames [][]string `json:"frames"`
	}{"transform-cache", calls, frames}); err != nil {
		return err
	}
	var backgrounds []string
	md = tui.NewMarkdownWithOptions("alpha\nbeta", 1, 2, theme(), &tui.DefaultTextStyle{BgColor: func(s string) string { backgrounds = append(backgrounds, s); return "\x1b[44m" + s + "\x1b[49m" }}, nil)
	rows := md.Render(12)
	if err := enc.Encode(struct {
		Name  string   `json:"name"`
		Calls []string `json:"calls"`
		Lines []string `json:"lines"`
	}{"background-order", backgrounds, rows}); err != nil {
		return err
	}
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	if err := emit("stock-nested-style", tui.NewMarkdownWithOptions("**nested**", 0, 0, nil, &tui.DefaultTextStyle{Bold: true}, nil).Render(24)); err != nil {
		return err
	}
	if err := emit("user-preserve-options", tui.NewUserMessageComponent("1. first\n1. second\n\n\"\\\"", nil, 1, nil).Render(24)); err != nil {
		return err
	}
	if err := emit("user-zones", tui.NewUserMessageComponent("hello", nil, 1, nil).Render(20)); err != nil {
		return err
	}
	var order []string
	var widths []int
	user := tui.NewUserMessageComponent("The input is $x^2$.", nil, 1, nil)
	user.SetMarkdownTransform(func(s string, w int) string {
		order = append(order, "formula")
		widths = append(widths, w)
		s = strings.ReplaceAll(s, "$x^2$", "x²")
		order = append(order, "suffix")
		return s + " Done."
	})
	rows = user.Render(80)
	if err := enc.Encode(struct {
		Name   string   `json:"name"`
		Calls  []string `json:"calls"`
		Widths []int    `json:"widths"`
		Lines  []string `json:"lines"`
	}{"user-transform", order, widths, rows}); err != nil {
		return err
	}
	suffix := "before"
	user = tui.NewUserMessageComponent("Message", nil, 1, nil)
	user.SetMarkdownTransform(func(s string, _ int) string { return s + " " + suffix })
	before := user.Render(80)
	suffix = "after"
	user.Invalidate()
	after := user.Render(80)
	if err := enc.Encode(struct {
		Name   string     `json:"name"`
		Frames [][]string `json:"frames"`
	}{"user-invalidation", [][]string{before, after}}); err != nil {
		return err
	}
	suffix = "before"
	user = tui.NewUserMessageComponent("Message", nil, 1, nil)
	var workers sync.WaitGroup
	user.SetMarkdownTransformState(func() string { return suffix })
	user.SetAsyncMarkdownTransform(&tui.AsyncMarkdownTransform{
		Context: context.Background(), Start: workers.Go,
		Prepare: func(source string, _ int) func(context.Context) string {
			value := source + " " + suffix
			return func(context.Context) string { return value }
		},
	})
	var paddingFrames [][]string
	for _, padding := range []int{1, 0, 1} {
		user.SetOutputPad(padding)
		user.Render(24)
		workers.Wait()
		paddingFrames = append(paddingFrames, user.Render(24))
		suffix = "after"
	}
	if err := enc.Encode(struct {
		Name   string     `json:"name"`
		Frames [][]string `json:"frames"`
	}{"user-padding-transform", paddingFrames}); err != nil {
		return err
	}
	assistant := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	assistant.SetContent([]tui.AssistantSegment{{Text: "日本語テスト hello world 你好世界 test"}})
	assistant.SetHasToolCalls(true)
	return emit("assistant-padding", assistant.Render(32))
}
