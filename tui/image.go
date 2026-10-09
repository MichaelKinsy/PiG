package tui

import (
	"container/list"
	"sync"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ImageTranscoder converts base64 image data to base64 PNG data, or reports
// false if it cannot. It is called synchronously during rendering.
// upstream: packages/tui/src/components/image.ts:ImageTranscoder
type ImageTranscoder func(base64Data, mimeType string) (string, bool)

// pngCacheCap bounds the shared conversions.
// upstream: packages/tui/src/components/image.ts:toPng
const pngCacheCap = 32

type pngCacheEntry struct {
	source string
	png    string
	ok     bool
}

var (
	imageTranscoderMu sync.Mutex
	imageTranscoder   ImageTranscoder
	// pngCache is a backstop for callers that recreate Image instances, keyed by source data, least recently used first.
	pngCache      = list.New()
	pngCacheIndex = map[string]*list.Element{}
)

// SetImageTranscoder registers the converter used for non-PNG images on
// Kitty-protocol terminals, which only accept PNG. Without one, such images
// render as text fallbacks. It clears the shared conversions.
// upstream: packages/tui/src/components/image.ts:setImageTranscoder
func SetImageTranscoder(transcoder ImageTranscoder) {
	imageTranscoderMu.Lock()
	defer imageTranscoderMu.Unlock()
	imageTranscoder = transcoder
	pngCache.Init()
	clear(pngCacheIndex)
}

// ImageTranscoderRegistered reports whether an image transcoder is registered.
func ImageTranscoderRegistered() bool {
	imageTranscoderMu.Lock()
	defer imageTranscoderMu.Unlock()
	return imageTranscoder != nil
}

var imageTranscoderLoader func(onRegistered func())

// SetImageTranscoderLoader installs the host function that registers the PNG transcoder for Kitty-protocol terminals
// (coding-agent utils/image-convert.ts ensurePngTranscoder). A tool card calls it when it shows a non-PNG image, so the
// transcoder is available in any host. The loader runs onRegistered once the transcoder is newly registered, and not when
// it was already registered or cannot load. Set it at start-up, before a card renders.
func SetImageTranscoderLoader(loader func(onRegistered func())) {
	imageTranscoderLoader = loader
}

// ensureImageTranscoder asks the installed loader to register the transcoder; with no loader, non-PNG images keep their
// text fallbacks.
func ensureImageTranscoder(onRegistered func()) {
	if imageTranscoderLoader != nil {
		imageTranscoderLoader(onRegistered)
	}
}

// toPng converts through the registered transcoder and the shared cache.
func toPng(base64Data, mimeType string) (string, bool) {
	imageTranscoderMu.Lock()
	defer imageTranscoderMu.Unlock()
	if imageTranscoder == nil {
		return "", false
	}
	var entry pngCacheEntry
	if element, cached := pngCacheIndex[base64Data]; cached {
		entry = element.Value.(pngCacheEntry)
		pngCache.Remove(element)
	} else {
		png, ok := imageTranscoder(base64Data, mimeType)
		entry = pngCacheEntry{source: base64Data, png: png, ok: ok}
	}
	pngCacheIndex[base64Data] = pngCache.PushBack(entry)
	if pngCache.Len() > pngCacheCap {
		oldest := pngCache.Front()
		pngCache.Remove(oldest)
		delete(pngCacheIndex, oldest.Value.(pngCacheEntry).source)
	}
	return entry.png, entry.ok
}

type ImageTheme struct {
	FallbackColor func(string) string
}

type ImageOptions struct {
	MaxWidthCells  int
	MaxHeightCells int
	Filename       string
	ImageID        int
}

type Image struct {
	invalidatable
	Base64Data string
	MIMEType   string
	Dimensions ImageDimensions
	Theme      ImageTheme
	Options    ImageOptions

	cachedLines []string
	cachedWidth int
	imageID     int
	// pngData is the converted PNG data for Kitty. Failures are not stored so a later transcoder can retry.
	pngData string
}

// DefaultImageTheme is the theme-aware ImageTheme: the fallback text is muted in the active theme.
func DefaultImageTheme() ImageTheme {
	th := ActiveTheme()
	return ImageTheme{FallbackColor: func(s string) string {
		if th.Muted != "" {
			return th.Muted + s + th.Reset
		}
		return s
	}}
}

// NewImage creates an image component with Pi's constructor shape (base64Data, mimeType, theme, options, dimensions). Dimensions default to the data's own size, or 800x600 when it cannot be read; an ImageTheme without FallbackColor styles nothing.
// upstream: packages/tui/src/components/image.ts constructor
func NewImage(base64Data, mimeType string, theme ImageTheme, options ImageOptions, dimensions *ImageDimensions) *Image {
	dims := ImageDimensions{WidthPx: 800, HeightPx: 600}
	if dimensions != nil {
		dims = *dimensions
	} else if got := GetImageDimensions(base64Data, mimeType); got != nil {
		dims = *got
	}
	if theme.FallbackColor == nil {
		theme = ImageTheme{FallbackColor: func(text string) string { return text }}
	}
	return &Image{
		Base64Data: base64Data,
		MIMEType:   mimeType,
		Dimensions: dims,
		Theme:      theme,
		Options:    options,
		imageID:    options.ImageID,
	}
}

func (i *Image) GetImageID() int { return i.imageID }

func (i *Image) Invalidate() {
	i.invalidatable.Invalidate()
	i.cachedLines = nil
	i.cachedWidth = 0
}

// imageMaxWidthCells is the width in cells an image draws at in width
// columns with the maxWidthCells option, 60 when it is unset
// (image.ts render: Math.max(1, Math.min(width - 2, maxWidthCells ?? 60))).
func imageMaxWidthCells(width, maxWidthCells int) int {
	if maxWidthCells <= 0 {
		maxWidthCells = 60
	}
	return max(1, min(width-2, maxWidthCells))
}

// Render returns image protocol rows or a width-bounded, styled fallback.
func (i *Image) Render(width int) []string {
	if i.cachedLines != nil && i.cachedWidth == width {
		return i.cachedLines
	}
	// Mirrors upstream image.ts:66-98.
	maxWidth := imageMaxWidthCells(width, i.Options.MaxWidthCells)
	cellDimensions := GetCellDimensions()
	defaultMaxHeight := max(1, (maxWidth*cellDimensions.WidthPx+cellDimensions.HeightPx-1)/cellDimensions.HeightPx)
	maxHeight := i.Options.MaxHeightCells
	if maxHeight <= 0 {
		maxHeight = defaultMaxHeight
	}

	caps := GetCapabilities()
	data, hasData := i.Base64Data, true
	dimensions := i.Dimensions
	if caps.Images == ImageProtocolKitty && i.MIMEType != "image/png" {
		if i.pngData == "" {
			if png, ok := toPng(i.Base64Data, i.MIMEType); ok {
				i.pngData = png
			}
		}
		data, hasData = i.pngData, i.pngData != ""
		// Conversion may apply EXIF rotation, so prefer the PNG's own dimensions.
		if hasData {
			if png := GetPNGDimensions(data); png != nil {
				dimensions = *png
			}
		}
	}
	var lines []string
	if caps.Images != "" && hasData {
		if caps.Images == ImageProtocolKitty && i.imageID == 0 {
			i.imageID = AllocateImageID()
		}
		result := RenderImage(data, dimensions, ImageRenderOptions{
			MaxWidthCells:  maxWidth,
			MaxHeightCells: maxHeight,
			ImageID:        i.imageID,
			MoveCursor:     new(false),
		})
		if result != nil {
			if result.ImageID != 0 {
				i.imageID = result.ImageID
			}
			if caps.Images == ImageProtocolKitty {
				lines = []string{result.Sequence}
				for range max(result.Rows-1, 0) {
					lines = append(lines, "")
				}
			} else {
				for range max(result.Rows-1, 0) {
					lines = append(lines, "")
				}
				rowOffset := result.Rows - 1
				moveUp := ""
				if rowOffset > 0 {
					moveUp = "\x1b[" + itoa(rowOffset) + "A"
				}
				lines = append(lines, moveUp+result.Sequence)
			}
		} else {
			lines = []string{widthx.TruncateToWidth(i.Theme.FallbackColor(ImageFallback(i.MIMEType, &i.Dimensions, i.Options.Filename)), width, "...", false)}
		}
	} else {
		lines = []string{widthx.TruncateToWidth(i.Theme.FallbackColor(ImageFallback(i.MIMEType, &i.Dimensions, i.Options.Filename)), width, "...", false)}
	}
	i.cachedLines = lines
	i.cachedWidth = width
	return lines
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
