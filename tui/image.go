package tui

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
}

func NewImage(base64Data, mimeType string, options ImageOptions, dimensions *ImageDimensions) *Image {
	dims := ImageDimensions{WidthPx: 800, HeightPx: 600}
	if dimensions != nil {
		dims = *dimensions
	} else if got := GetImageDimensions(base64Data, mimeType); got != nil {
		dims = *got
	}
	th := ActiveTheme()
	return &Image{
		Base64Data: base64Data,
		MIMEType:   mimeType,
		Dimensions: dims,
		Theme: ImageTheme{FallbackColor: func(s string) string {
			if th.Muted != "" {
				return th.Muted + s + th.Reset
			}
			return s
		}},
		Options: options,
		imageID: options.ImageID,
	}
}

func (i *Image) GetImageID() int { return i.imageID }

func (i *Image) Invalidate() {
	i.invalidatable.Invalidate()
	i.cachedLines = nil
	i.cachedWidth = 0
}

func (i *Image) Render(width int) []string {
	if i.cachedLines != nil && i.cachedWidth == width {
		return i.cachedLines
	}
	// Mirrors upstream image.ts:66-98.
	maxCells := i.Options.MaxWidthCells
	if maxCells <= 0 {
		maxCells = 60
	}
	maxWidth := max(1, min(width-2, maxCells))
	cellDimensions := GetCellDimensions()
	defaultMaxHeight := max(1, (maxWidth*cellDimensions.WidthPx+cellDimensions.HeightPx-1)/cellDimensions.HeightPx)
	maxHeight := i.Options.MaxHeightCells
	if maxHeight <= 0 {
		maxHeight = defaultMaxHeight
	}

	caps := GetCapabilities()
	var lines []string
	if caps.Images != "" {
		if caps.Images == ImageProtocolKitty && i.imageID == 0 {
			i.imageID = AllocateImageID()
		}
		result := RenderImage(i.Base64Data, i.Dimensions, ImageRenderOptions{
			MaxWidthCells:  maxWidth,
			MaxHeightCells: maxHeight,
			ImageID:        i.imageID,
			MoveCursor:     false,
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
			lines = []string{i.Theme.FallbackColor(ImageFallback(i.MIMEType, &i.Dimensions, i.Options.Filename))}
		}
	} else {
		lines = []string{i.Theme.FallbackColor(ImageFallback(i.MIMEType, &i.Dimensions, i.Options.Filename))}
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
