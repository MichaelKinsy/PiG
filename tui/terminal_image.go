package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type ImageProtocol string

const (
	ImageProtocolKitty  ImageProtocol = "kitty"
	ImageProtocolITerm2 ImageProtocol = "iterm2"
)

type TerminalCapabilities struct {
	Images     ImageProtocol
	TrueColor  bool
	Hyperlinks bool
}

type CellDimensions struct {
	WidthPx  int
	HeightPx int
}

type ImageDimensions struct {
	WidthPx  int
	HeightPx int
}

type ImageRenderOptions struct {
	MaxWidthCells       int
	MaxHeightCells      int
	PreserveAspectRatio bool
	ImageID             int
	Name                string
	MoveCursor          bool // if false, Kitty C=1 suppresses terminal-side cursor movement
}

type renderedImage struct {
	Sequence string
	Rows     int
	ImageID  int
}

// CapabilityOverrides mirrors upstream Partial<TerminalCapabilities> as passed
// to setCapabilityOverrides. A nil field is absent. Images points at "" for
// upstream's null (no image protocol).
type CapabilityOverrides struct {
	Images     *ImageProtocol
	TrueColor  *bool
	Hyperlinks *bool
}

func (o CapabilityOverrides) equal(other CapabilityOverrides) bool {
	return ptrValueEqual(o.Images, other.Images) &&
		ptrValueEqual(o.TrueColor, other.TrueColor) &&
		ptrValueEqual(o.Hyperlinks, other.Hyperlinks)
}

func ptrValueEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (o CapabilityOverrides) clone() CapabilityOverrides {
	return CapabilityOverrides{Images: clonePtr(o.Images), TrueColor: clonePtr(o.TrueColor), Hyperlinks: clonePtr(o.Hyperlinks)}
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

var (
	// capabilityMu makes override replacement and cache invalidation one
	// operation. The cache remains atomic because renderers read it often.
	capabilityMu         sync.Mutex
	cachedCapabilities   atomic.Pointer[TerminalCapabilities]
	capabilityOverrides  CapabilityOverrides
	cellDimensions       = CellDimensions{WidthPx: 9, HeightPx: 18}
	tmuxHyperlinkProbe   = probeTmuxHyperlinks
	tmuxProbeTimeout     = 250 * time.Millisecond // upstream: terminal-image.ts:probeTmuxHyperlinks
	capabilityDetectGOOS = runtime.GOOS
)

func GetCellDimensions() CellDimensions { return cellDimensions }
func SetCellDimensions(dims CellDimensions) {
	cellDimensions = dims
}

// probeTmuxHyperlinks mirrors upstream probeTmuxHyperlinks: tmux re-emits OSC 8
// only when the attached client's client_termfeatures lists hyperlinks. Any
// error, including the 250ms timeout, reports false.
func probeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return tmuxTermfeaturesIncludeHyperlinks(string(out))
}

func tmuxTermfeaturesIncludeHyperlinks(termfeatures string) bool {
	for feature := range strings.SplitSeq(termfeatures, ",") {
		if strings.TrimSpace(feature) == "hyperlinks" {
			return true
		}
	}
	return false
}

// detectCapabilitiesFromEnvironment mirrors upstream
// detectCapabilitiesFromEnvironment. goos stands in for process.platform.
func detectCapabilitiesFromEnvironment(tmuxForwardsHyperlink func() bool, goos string) TerminalCapabilities {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	terminalEmulator := strings.ToLower(os.Getenv("TERMINAL_EMULATOR"))
	term := strings.ToLower(os.Getenv("TERM"))
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit"
	isWindowsConsole := goos == "windows"

	// pig divergence (D44): require Herdr's explicit image-forwarding signal.
	if os.Getenv("HERDR_ENV") == "1" {
		if os.Getenv("HERDR_KITTY_GRAPHICS") == "1" {
			return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
		}
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
	}
	// Emit OSC 8 hyperlinks only when tmux confirms it forwards. Image
	// protocols are unreliable under tmux, so leave images off.
	if os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux") {
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: tmuxForwardsHyperlink()}
	}
	// screen does not forward OSC 8 hyperlinks, so keep them off there.
	if strings.HasPrefix(term, "screen") {
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" || termProgram == "kitty" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}
	if termProgram == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}
	if os.Getenv("WEZTERM_PANE") != "" || termProgram == "wezterm" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}
	// Warp supports the Kitty graphics protocol and OSC 8 hyperlinks.
	if termProgram == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}
	if os.Getenv("ITERM_SESSION_ID") != "" || termProgram == "iterm.app" {
		return TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}
	}
	if os.Getenv("WT_SESSION") != "" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}
	if termProgram == "alacritty" || termProgram == "vscode" || termProgram == "zed" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}
	if terminalEmulator == "jetbrains-jediterm" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: false}
	}
	// Windows Terminal does not always set WT_SESSION. Modern Windows consoles
	// support truecolor; keep hyperlinks off unless positively detected above.
	if isWindowsConsole {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: false}
	}
	// Unknown terminal: be conservative about OSC 8, which terminals that
	// swallow it render as bare text with the URL gone.
	return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
}

// parseBooleanCapabilityOverride mirrors upstream: only "1" and "0" override.
func parseBooleanCapabilityOverride(value string) *bool {
	switch value {
	case "1":
		return new(true)
	case "0":
		return new(false)
	}
	return nil
}

// DetectCapabilities mirrors upstream detectCapabilities: PI_HYPERLINKS,
// PI_IMAGE_PROTOCOL, and PI_TRUE_COLOR override auto-detection, and an
// explicit PI_HYPERLINKS replaces the tmux probe. A nil tmuxForwardsHyperlink
// uses the default tmux probe.
func DetectCapabilities(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	if tmuxForwardsHyperlink == nil {
		tmuxForwardsHyperlink = tmuxHyperlinkProbe
	}
	hyperlinks := parseBooleanCapabilityOverride(os.Getenv("PI_HYPERLINKS"))
	probe := tmuxForwardsHyperlink
	if hyperlinks != nil {
		probe = func() bool { return *hyperlinks }
	}
	detected := detectCapabilitiesFromEnvironment(probe, capabilityDetectGOOS)
	switch imageProtocol := strings.ToLower(os.Getenv("PI_IMAGE_PROTOCOL")); imageProtocol {
	case "kitty", "iterm2":
		detected.Images = ImageProtocol(imageProtocol)
	case "none", "0":
		detected.Images = ""
	}
	if trueColor := parseBooleanCapabilityOverride(os.Getenv("PI_TRUE_COLOR")); trueColor != nil {
		detected.TrueColor = *trueColor
	}
	if hyperlinks != nil {
		detected.Hyperlinks = *hyperlinks
	}
	return detected
}

// GetCapabilities mirrors upstream getCapabilities: detection with the PI_*
// environment, then the settings overrides on top.
func GetCapabilities() TerminalCapabilities {
	capabilityMu.Lock()
	defer capabilityMu.Unlock()
	if cached := cachedCapabilities.Load(); cached != nil {
		return *cached
	}
	overrides := capabilityOverrides
	var probe func() bool
	if overrides.Hyperlinks != nil {
		hyperlinks := *overrides.Hyperlinks
		probe = func() bool { return hyperlinks }
	}
	caps := DetectCapabilities(probe)
	if overrides.Images != nil {
		caps.Images = *overrides.Images
	}
	if overrides.TrueColor != nil {
		caps.TrueColor = *overrides.TrueColor
	}
	if overrides.Hyperlinks != nil {
		caps.Hyperlinks = *overrides.Hyperlinks
	}
	cachedCapabilities.Store(&caps)
	return caps
}

func ResetCapabilitiesCache() {
	capabilityMu.Lock()
	cachedCapabilities.Store(nil)
	capabilityMu.Unlock()
}

// SetCapabilityOverrides mirrors upstream setCapabilityOverrides: it replaces
// the overrides and drops the cache only when a field changed.
func SetCapabilityOverrides(overrides CapabilityOverrides) {
	capabilityMu.Lock()
	defer capabilityMu.Unlock()
	if capabilityOverrides.equal(overrides) {
		return
	}
	capabilityOverrides = overrides.clone()
	cachedCapabilities.Store(nil)
}

// SetCapabilities overrides the cached capabilities. Mirrors upstream
// setCapabilities.
func SetCapabilities(caps TerminalCapabilities) {
	capabilityMu.Lock()
	cachedCapabilities.Store(&caps)
	capabilityMu.Unlock()
}

// IsImageLine reports whether the line contains Kitty or iTerm2 image protocol bytes.
// Mirrors upstream terminal-image.ts isImageLine().
func IsImageLine(line string) bool { return widthx.IsImageLine(line) }

func AllocateImageID() int {
	return int(rand.Uint32()%0xfffffffe) + 1
}

func EncodeKitty(base64Data string, columns, rows, imageID int, moveCursor ...bool) string {
	const chunkSize = 4096
	params := []string{"a=T", "f=100", "q=2"}
	if columns > 0 {
		params = append(params, fmt.Sprintf("c=%d", columns))
	}
	if rows > 0 {
		params = append(params, fmt.Sprintf("r=%d", rows))
	}
	if imageID > 0 {
		params = append(params, fmt.Sprintf("i=%d", imageID))
	}
	// C=1 suppresses Kitty's built-in cursor movement after placement
	if len(moveCursor) > 0 && !moveCursor[0] {
		params = append(params, "C=1")
	}
	if len(base64Data) <= chunkSize {
		return "\x1b_G" + strings.Join(params, ",") + ";" + base64Data + "\x1b\\"
	}
	var chunks []string
	for off := 0; off < len(base64Data); off += chunkSize {
		end := min(off+chunkSize, len(base64Data))
		chunk := base64Data[off:end]
		switch {
		case off == 0:
			chunks = append(chunks, "\x1b_G"+strings.Join(params, ",")+",m=1;"+chunk+"\x1b\\")
		case end == len(base64Data):
			chunks = append(chunks, "\x1b_Gm=0;"+chunk+"\x1b\\")
		default:
			chunks = append(chunks, "\x1b_Gm=1;"+chunk+"\x1b\\")
		}
	}
	return strings.Join(chunks, "")
}

func DeleteKittyImage(imageID int) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", imageID)
}

func DeleteAllKittyImages() string { return "\x1b_Ga=d,d=A,q=2\x1b\\" }

// DeleteAllKittyPlacements removes every Kitty image placement (visible
// renderings) while leaving the transmitted image data intact. Mirrors upstream
// deleteAllKittyPlacements.
func DeleteAllKittyPlacements() string { return "\x1b_Ga=d,d=a,q=2\x1b\\" }

func EncodeITerm2(base64Data string, width any, height any, name string, preserveAspect bool) string {
	params := []string{"inline=1"}
	if width != nil {
		params = append(params, fmt.Sprintf("width=%v", width))
	}
	if height != nil {
		params = append(params, fmt.Sprintf("height=%v", height))
	}
	if name != "" {
		params = append(params, "name="+base64.StdEncoding.EncodeToString([]byte(name)))
	}
	if !preserveAspect {
		params = append(params, "preserveAspectRatio=0")
	}
	return "\x1b]1337;File=" + strings.Join(params, ";") + ":" + base64Data + "\x07"
}

type ImageCellSize struct {
	Columns int
	Rows    int
}

func CalculateImageCellSize(imageDimensions ImageDimensions, maxWidthCells int, maxHeightCells int, dims CellDimensions) ImageCellSize {
	maxWidth := max(1, maxWidthCells)
	imageWidth := max(1, imageDimensions.WidthPx)
	imageHeight := max(1, imageDimensions.HeightPx)

	widthScale := float64(maxWidth*dims.WidthPx) / float64(imageWidth)
	heightScale := widthScale
	if maxHeightCells > 0 {
		heightScale = float64(maxHeightCells*dims.HeightPx) / float64(imageHeight)
	}
	scale := min(widthScale, heightScale)

	scaledWidthPx := float64(imageWidth) * scale
	scaledHeightPx := float64(imageHeight) * scale
	columns := max(1, min(maxWidth, int(math.Ceil(scaledWidthPx/float64(dims.WidthPx)))))
	rows := max(1, int(math.Ceil(scaledHeightPx/float64(dims.HeightPx))))
	if maxHeightCells > 0 && rows > maxHeightCells {
		rows = maxHeightCells
	}

	return ImageCellSize{Columns: columns, Rows: rows}
}

func CalculateImageRows(imageDimensions ImageDimensions, targetWidthCells int, dims CellDimensions) int {
	return CalculateImageCellSize(imageDimensions, targetWidthCells, 0, dims).Rows
}

func GetPNGDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil || len(buf) < 24 {
		return nil
	}
	if buf[0] != 0x89 || buf[1] != 0x50 || buf[2] != 0x4e || buf[3] != 0x47 {
		return nil
	}
	width := int(buf[16])<<24 | int(buf[17])<<16 | int(buf[18])<<8 | int(buf[19])
	height := int(buf[20])<<24 | int(buf[21])<<16 | int(buf[22])<<8 | int(buf[23])
	return &ImageDimensions{WidthPx: width, HeightPx: height}
}

func GetJPEGDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil || len(buf) < 2 || buf[0] != 0xff || buf[1] != 0xd8 {
		return nil
	}
	for offset := 2; offset < len(buf)-9; {
		if buf[offset] != 0xff {
			offset++
			continue
		}
		marker := buf[offset+1]
		if marker >= 0xc0 && marker <= 0xc2 {
			height := int(buf[offset+5])<<8 | int(buf[offset+6])
			width := int(buf[offset+7])<<8 | int(buf[offset+8])
			return &ImageDimensions{WidthPx: width, HeightPx: height}
		}
		if offset+3 >= len(buf) {
			return nil
		}
		length := int(buf[offset+2])<<8 | int(buf[offset+3])
		if length < 2 {
			return nil
		}
		offset += 2 + length
	}
	return nil
}

func GetGIFDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil || len(buf) < 10 {
		return nil
	}
	sig := string(buf[:6])
	if sig != "GIF87a" && sig != "GIF89a" {
		return nil
	}
	width := int(buf[6]) | int(buf[7])<<8
	height := int(buf[8]) | int(buf[9])<<8
	return &ImageDimensions{WidthPx: width, HeightPx: height}
}

func GetWebPDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil || len(buf) < 30 {
		return nil
	}
	if string(buf[:4]) != "RIFF" || string(buf[8:12]) != "WEBP" {
		return nil
	}
	chunk := string(buf[12:16])
	switch chunk {
	case "VP8 ":
		width := (int(buf[26]) | int(buf[27])<<8) & 0x3fff
		height := (int(buf[28]) | int(buf[29])<<8) & 0x3fff
		return &ImageDimensions{WidthPx: width, HeightPx: height}
	case "VP8L":
		bits := int(buf[21]) | int(buf[22])<<8 | int(buf[23])<<16 | int(buf[24])<<24
		width := (bits & 0x3fff) + 1
		height := ((bits >> 14) & 0x3fff) + 1
		return &ImageDimensions{WidthPx: width, HeightPx: height}
	case "VP8X":
		width := (int(buf[24]) | int(buf[25])<<8 | int(buf[26])<<16) + 1
		height := (int(buf[27]) | int(buf[28])<<8 | int(buf[29])<<16) + 1
		return &ImageDimensions{WidthPx: width, HeightPx: height}
	default:
		return nil
	}
}

func GetImageDimensions(base64Data, mimeType string) *ImageDimensions {
	switch mimeType {
	case "image/png":
		return GetPNGDimensions(base64Data)
	case "image/jpeg":
		return GetJPEGDimensions(base64Data)
	case "image/gif":
		return GetGIFDimensions(base64Data)
	case "image/webp":
		return GetWebPDimensions(base64Data)
	default:
		return nil
	}
}

func RenderImage(base64Data string, imageDimensions ImageDimensions, options ImageRenderOptions) *renderedImage {
	caps := GetCapabilities()
	if caps.Images == "" {
		return nil
	}
	maxWidth := options.MaxWidthCells
	if maxWidth <= 0 {
		maxWidth = 80
	}
	size := CalculateImageCellSize(imageDimensions, maxWidth, options.MaxHeightCells, GetCellDimensions())
	switch caps.Images {
	case ImageProtocolKitty:
		if options.ImageID > 0 {
			RegisterKittyImageMetadata(KittyImageMetadata{
				ImageID:  options.ImageID,
				Columns:  size.Columns,
				Rows:     size.Rows,
				WidthPx:  imageDimensions.WidthPx,
				HeightPx: imageDimensions.HeightPx,
			})
		}
		seq := EncodeKitty(base64Data, size.Columns, size.Rows, options.ImageID, options.MoveCursor)
		return &renderedImage{Sequence: seq, Rows: size.Rows, ImageID: options.ImageID}
	case ImageProtocolITerm2:
		preserve := true
		if !options.PreserveAspectRatio {
			preserve = false
		}
		seq := EncodeITerm2(base64Data, size.Columns, "auto", "", preserve)
		return &renderedImage{Sequence: seq, Rows: size.Rows}
	default:
		return nil
	}
}

func Hyperlink(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func ImageFallback(mimeType string, dimensions *ImageDimensions, filename string) string {
	parts := []string{}
	if filename != "" {
		parts = append(parts, filename)
	}
	parts = append(parts, "["+mimeType+"]")
	if dimensions != nil {
		parts = append(parts, fmt.Sprintf("%dx%d", dimensions.WidthPx, dimensions.HeightPx))
	}
	return "[Image: " + strings.Join(parts, " ") + "]"
}
