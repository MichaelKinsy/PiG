package tui

// pi: packages/tui/src/index.ts

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/latex"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// barrelGo maps every runtime export of Pi's packages/tui/src/index.ts to the Go declaration the interface ledger names for it. A map of
// values and typed nils makes each counterpart a compile-time reference: a renamed or removed Go symbol breaks this file.
var barrelGo = map[string]any{
	"Box": (*Box)(nil), "CURSOR_MARKER": widthx.CursorMarker, "CancellableLoader": (*CancellableLoader)(nil),
	"CombinedAutocompleteProvider": (*CombinedProvider)(nil), "Container": (*Container)(nil), "Editor": (*Editor)(nil), "HStack": (*HStack)(nil),
	"Image": (*Image)(nil), "Input": (*TextInput)(nil), "Key": Key, "KeybindingsManager": (*TUIKeybindingsManager)(nil), "Loader": (*Loader)(nil),
	"Markdown": (*Markdown)(nil), "MouseRegion": (*MouseRegion)(nil), "ProcessTerminal": (*ProcessTerminal)(nil), "ScrollView": (*ScrollView)(nil),
	"SelectList": (*SelectList)(nil), "SettingsList": (*SettingsList)(nil), "Spacer": (*Spacer)(nil), "StdinBuffer": (*StdinBuffer)(nil),
	"TUI_KEYBINDINGS": TUIKeybindings, "Text": (*Text)(nil), "TruncatedText": (*TruncatedText)(nil), "TuiAltScreen": (*TuiAltScreen)(nil),
	"TuiMainScreen": (*TuiMainScreen)(nil), "VStack": (*VStack)(nil),
	"allocateImageId": AllocateImageID, "backgroundAnsi": BackgroundAnsi, "calculateImageRows": CalculateImageRows, "colorToHex": ColorToHex,
	"colorToOkhsl": ColorToOkhsl, "colorToOklch": ColorToOklch, "colorToRgb": ColorToRgb, "compositeTuiLine": CompositeTuiLine,
	"decodeKittyPrintable": DecodeKittyPrintable, "deleteAllKittyImages": DeleteAllKittyImages, "deleteKittyImage": DeleteKittyImage,
	"detectCapabilities": DetectCapabilities, "encodeITerm2": EncodeITerm2, "encodeKitty": EncodeKitty, "foregroundAnsi": ForegroundAnsi,
	"formatProgramStatus": FormatProgramStatus, "fuzzyFilter": FuzzyFilter[string], "fuzzyMatch": FuzzyMatchScore, "getCapabilities": GetCapabilities,
	"getCellDimensions": GetCellDimensions, "getGifDimensions": GetGIFDimensions, "getImageDimensions": GetImageDimensions,
	"getJpegDimensions": GetJPEGDimensions, "getKeybindings": GetKeybindings, "getNativeClipboard": GetNativeClipboard,
	"getOsc8LinkAtColumn": widthx.GetOsc8LinkAtColumn, "getPngDimensions": GetPNGDimensions, "getSelectPageIndex": GetSelectPageIndex,
	"getTerminalColorMode": GetTerminalColorMode, "getWebpDimensions": GetWebPDimensions, "hyperlink": Hyperlink, "imageFallback": ImageFallback,
	"indexedColor": NewIndexedColor, "isAppleTerminalSession": IsAppleTerminalSession, "isFocusable": IsFocusable, "isKeyRelease": IsKeyRelease,
	"isKeyRepeat": IsKeyRepeat, "isKittyProtocolActive": IsKittyProtocolActive, "isViewportTUI": IsViewportTUI, "matchesKey": MatchesKeyID,
	"mixColors": MixColors, "okhslColor": NewOkhslColor, "oklabToOkhslLightness": OklabToOkhslLightness, "oklchColor": NewOklchColor,
	"parseColor": ParseColor[string], "parseKey": ParseKey, "parseTerminalColorSchemeReport": ParseTerminalColorSchemeReport,
	"renderFakeCursor": widthx.RenderFakeCursor, "renderImage": RenderImage, "renderLatex": latex.RenderLatex,
	"replaceControlCharacters": widthx.ReplaceControlCharacters, "resetCapabilitiesCache": ResetCapabilitiesCache, "rgbColor": NewRgbColor,
	"setCapabilities": SetCapabilities, "setCapabilityOverrides": SetCapabilityOverrides, "setCellDimensions": SetCellDimensions,
	"setImageTranscoder": SetImageTranscoder, "setKeybindings": SetKeybindings, "setKittyProtocolActive": SetKittyProtocolActive,
	"sliceByColumn": widthx.SliceByColumn, "stripTerminalSequences": widthx.StripTerminalSequences, "styleText": StyleText,
	"styleTextWithAnsi": StyleTextWithAnsi, "truncateToWidth": widthx.TruncateToWidth, "visibleWidth": widthx.VisibleWidth,
	"wrapTextWithAnsi": widthx.WrapTextWithAnsi,
}

// barrelDesignedOut are the runtime exports of the barrel with no Go declaration, as the interface ledger records them.
var barrelDesignedOut = map[string]string{
	"Marked": "the barrel re-exports the third-party marked parser class; the Go markdown renderer has its own lexer and exposes no marked type",
}

type barrelCase struct {
	Fn   string `json:"fn"`
	Args []any  `json:"args"`
	Out  any    `json:"out"`
}

type barrelOracle struct {
	Names []string     `json:"names"`
	Cases []barrelCase `json:"cases"`
}

func barrelPi(t *testing.T) barrelOracle {
	t.Helper()
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "tui", "src", "index.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/index_exports.mjs", source)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "WT_SESSION=") || strings.HasPrefix(kv, "SSH_")
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var oracle barrelOracle
	if err := json.Unmarshal(output, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// Every runtime export of Pi's barrel has a Go counterpart or a recorded reason it has none, and the table names nothing the barrel does not
// export.
func TestIndexRuntimeExportsAreExactlyTheGoBarrel(t *testing.T) {
	oracle := barrelPi(t)
	want := map[string]bool{}
	for _, name := range oracle.Names {
		want[name] = true
		_, ported := barrelGo[name]
		_, designed := barrelDesignedOut[name]
		if ported == designed {
			t.Errorf("Pi exports %q: Go counterpart %v, designed out %v; exactly one must hold", name, ported, designed)
		}
	}
	var extra []string
	for name := range barrelGo {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	for name := range barrelDesignedOut {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("the Go barrel table names exports Pi's index.ts does not have: %v", extra)
	}
	if len(oracle.Names) < 90 {
		t.Errorf("Pi's barrel lists %d runtime exports; the oracle read a truncated namespace", len(oracle.Names))
	}
}

func argString(v any) string { s, _ := v.(string); return s }
func argInt(v any) int       { f, _ := v.(float64); return int(f) }
func argBool(v any) bool     { b, _ := v.(bool); return b }
func argDims(v any) ImageDimensions {
	m, _ := v.(map[string]any)
	return ImageDimensions{WidthPx: argInt(m["widthPx"]), HeightPx: argInt(m["heightPx"])}
}

// barrelGoResult calls the Go counterpart of the Pi export the case names with the case's arguments, shaped like the oracle's JSON result.
func barrelGoResult(t *testing.T, c barrelCase) any {
	t.Helper()
	a := c.Args
	optional := func(s string, ok bool) any {
		if !ok {
			return nil
		}
		return s
	}
	switch c.Fn {
	case "parseKey":
		return optional(ParseKey(argString(a[0])))
	case "decodeKittyPrintable":
		return optional(DecodeKittyPrintable(argString(a[0])))
	case "isKeyRelease":
		return IsKeyRelease(argString(a[0]))
	case "isKeyRepeat":
		return IsKeyRepeat(argString(a[0]))
	case "matchesKey":
		return MatchesKeyID(argString(a[0]), argString(a[1]))
	case "fuzzyMatch":
		m := FuzzyMatchScore(argString(a[0]), argString(a[1]))
		return map[string]any{"matches": m.Matches, "score": m.Score}
	case "fuzzyFilter":
		var items []string
		for _, item := range a[0].([]any) {
			items = append(items, item.(string))
		}
		out := []any{}
		for _, item := range FuzzyFilter(items, argString(a[1]), func(s string) string { return s }) {
			out = append(out, item)
		}
		return out
	case "parseTerminalColorSchemeReport":
		return optional(string(ParseTerminalColorSchemeReport(argString(a[0]))), ParseTerminalColorSchemeReport(argString(a[0])) != "")
	case "formatProgramStatus":
		m := a[0].(map[string]any)
		return FormatProgramStatus(ProgramStatus{State: ProgramState(argString(m["state"])), App: argString(m["app"]), Kind: ProgramStatusKind(argString(m["kind"])), Message: argString(m["message"])})
	case "oklabToOkhslLightness":
		return OklabToOkhslLightness(a[0].(float64))
	case "parseColor":
		color, err := ParseColor(argString(a[0]))
		if err != nil {
			return map[string]any{"error": true}
		}
		rgb := ColorToRgb(color)
		return map[string]any{
			"hex": ColorToHex(color), "rgb": map[string]any{"r": rgb.R, "g": rgb.G, "b": rgb.B},
			"fg": ForegroundAnsi(color, TerminalColorModeTrueColor), "bg": BackgroundAnsi(color, TerminalColorMode256),
		}
	case "hyperlink":
		return Hyperlink(argString(a[0]), argString(a[1]))
	case "calculateImageRows":
		cell := argDims(a[2])
		return CalculateImageRows(argDims(a[0]), argInt(a[1]), CellDimensions(cell))
	case "visibleWidth":
		return widthx.VisibleWidth(argString(a[0]))
	case "stripTerminalSequences":
		return widthx.StripTerminalSequences(argString(a[0]))
	case "replaceControlCharacters":
		return widthx.ReplaceControlCharacters(argString(a[0]))
	case "truncateToWidth":
		return widthx.TruncateToWidth(argString(a[0]), argInt(a[1]), argString(a[2]), argBool(a[3]))
	case "wrapTextWithAnsi":
		out := []any{}
		for _, line := range widthx.WrapTextWithAnsi(argString(a[0]), argInt(a[1])) {
			out = append(out, line)
		}
		return out
	case "sliceByColumn":
		return widthx.SliceByColumn(argString(a[0]), argInt(a[1]), argInt(a[2]), argBool(a[3]))
	case "getOsc8LinkAtColumn":
		return optional(widthx.GetOsc8LinkAtColumn(argString(a[0]), argInt(a[1])))
	}
	t.Fatalf("no Go counterpart is wired for the sampled export %q", c.Fn)
	return nil
}

// sameBarrelValue compares two decoded JSON values; numbers agree to a relative 1e-12, which the colour-space exports need.
func sameBarrelValue(got, want any) bool {
	g, gok := got.(float64)
	w, wok := want.(float64)
	if gok && wok {
		return g == w || math.Abs(g-w) <= 1e-12*math.Max(1, math.Abs(w))
	}
	switch want := want.(type) {
	case map[string]any:
		got, ok := got.(map[string]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for k, v := range want {
			if !sameBarrelValue(got[k], v) {
				return false
			}
		}
		return true
	case []any:
		got, ok := got.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if !sameBarrelValue(got[i], want[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

// The sampled pure exports of the barrel return what Pi's own export returns for the same arguments.
func TestIndexSampledExportsMatchPi(t *testing.T) {
	oracle := barrelPi(t)
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	if len(oracle.Cases) < 100 {
		t.Fatalf("the oracle produced %d cases", len(oracle.Cases))
	}
	seen := map[string]int{}
	for _, c := range oracle.Cases {
		seen[c.Fn]++
		raw, err := json.Marshal(barrelGoResult(t, c))
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !sameBarrelValue(got, c.Out) {
			t.Errorf("%s(%v) = %s, Pi gives %v", c.Fn, c.Args, raw, c.Out)
		}
	}
	for _, fn := range []string{"parseKey", "matchesKey", "decodeKittyPrintable", "isKeyRelease", "isKeyRepeat", "fuzzyMatch", "fuzzyFilter",
		"parseTerminalColorSchemeReport", "formatProgramStatus", "oklabToOkhslLightness", "parseColor", "hyperlink", "calculateImageRows",
		"visibleWidth", "truncateToWidth", "wrapTextWithAnsi", "sliceByColumn", "stripTerminalSequences", "replaceControlCharacters", "getOsc8LinkAtColumn"} {
		if seen[fn] == 0 {
			t.Errorf("the oracle sampled no %s case", fn)
		}
	}
}
