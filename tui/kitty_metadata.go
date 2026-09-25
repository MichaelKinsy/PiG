package tui

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Ports pi-tui's terminal-image.ts Kitty metadata registry and the two layout
// helpers (getKittyImageMetadata, cropKittyImageLine). Names and structure mirror
// upstream. Forced Go mechanics: the module-level Map<number, ...> becomes a
// mutex-guarded map plus an explicit insertion-order slice (Go maps have no
// insertion order, which upstream's LRU eviction relies on); the transmission
// generation counter and the 1000-entry cap match upstream exactly.
//
// The registry is populated by RenderImage's Kitty branch (mirroring upstream
// renderImage) and read by the layout engine when cropping a Kitty image that is
// partially scrolled out of a viewport.

// KittyImageMetadata is the public metadata for a registered Kitty image.
type KittyImageMetadata struct {
	ImageID  int
	Columns  int
	Rows     int
	WidthPx  int
	HeightPx int
}

type registeredKittyImageMetadata struct {
	KittyImageMetadata
	transmissionGeneration int
}

const kittyImageMetadataCap = 1000 // upstream: tui/src/terminal-image.ts:kittyImageMetadata

var (
	kittyMetadataMu             sync.Mutex
	kittyImageMetadata          = map[int]registeredKittyImageMetadata{}
	kittyImageMetadataOrder     []int // insertion order for LRU eviction
	kittyTransmissionGeneration int
)

// kittyGraphicPattern matches the APC \x1b_G...; control prefix; group 1 is the
// control string (up to the first ';').
var kittyGraphicPattern = regexp.MustCompile("\x1b_G([^;]*);")

// kittyImageIDPattern extracts i=<digits> from a Kitty control string.
var kittyImageIDPattern = regexp.MustCompile(`(?:^|,)i=(\d+)(?:,|$)`)

// kittyCropControlPattern matches the y/h/r controls a crop overrides.
var kittyCropControlPattern = regexp.MustCompile(`^[yhr]=`)

// RegisterKittyImageMetadata records metadata for a transmitted Kitty image,
// evicting the oldest entry past the cap. Mirrors upstream
// registerKittyImageMetadata.
func RegisterKittyImageMetadata(metadata KittyImageMetadata) {
	kittyMetadataMu.Lock()
	defer kittyMetadataMu.Unlock()
	kittyTransmissionGeneration++
	if _, exists := kittyImageMetadata[metadata.ImageID]; exists {
		removeKittyOrder(metadata.ImageID)
	}
	kittyImageMetadata[metadata.ImageID] = registeredKittyImageMetadata{
		KittyImageMetadata:     metadata,
		transmissionGeneration: kittyTransmissionGeneration,
	}
	kittyImageMetadataOrder = append(kittyImageMetadataOrder, metadata.ImageID)
	if len(kittyImageMetadata) > kittyImageMetadataCap {
		oldest := kittyImageMetadataOrder[0]
		kittyImageMetadataOrder = kittyImageMetadataOrder[1:]
		delete(kittyImageMetadata, oldest)
	}
}

func removeKittyOrder(imageID int) {
	for i, id := range kittyImageMetadataOrder {
		if id == imageID {
			kittyImageMetadataOrder = append(kittyImageMetadataOrder[:i], kittyImageMetadataOrder[i+1:]...)
			return
		}
	}
}

// getRegisteredKittyImageMetadata parses the imageId out of a rendered line and
// returns its registered metadata, or false if none. Mirrors upstream.
func getRegisteredKittyImageMetadata(line string) (registeredKittyImageMetadata, bool) {
	controls := kittyGraphicPattern.FindStringSubmatch(line)
	if controls == nil {
		return registeredKittyImageMetadata{}, false
	}
	id := kittyImageIDPattern.FindStringSubmatch(controls[1])
	if id == nil {
		return registeredKittyImageMetadata{}, false
	}
	imageID, err := strconv.Atoi(id[1])
	if err != nil {
		return registeredKittyImageMetadata{}, false
	}
	kittyMetadataMu.Lock()
	defer kittyMetadataMu.Unlock()
	m, ok := kittyImageMetadata[imageID]
	return m, ok
}

// GetKittyImageMetadata returns the public metadata for the Kitty image on a
// rendered line, or nil if the line carries no registered image. Mirrors
// upstream getKittyImageMetadata.
func GetKittyImageMetadata(line string) *KittyImageMetadata {
	m, ok := getRegisteredKittyImageMetadata(line)
	if !ok {
		return nil
	}
	meta := m.KittyImageMetadata
	return &meta
}

// CropKittyImageLine rewrites a Kitty image line to show only rows
// [hiddenRows, hiddenRows+visibleRows) of the source image, adjusting the y/h/r
// controls. Returns the line unchanged when it carries no registered image or
// the crop is a no-op. Mirrors upstream cropKittyImageLine.
func CropKittyImageLine(line string, hiddenRows, visibleRows int) string {
	metadata := GetKittyImageMetadata(line)
	loc := kittyGraphicPattern.FindStringSubmatchIndex(line)
	if metadata == nil || loc == nil || hiddenRows < 0 || hiddenRows >= metadata.Rows || visibleRows <= 0 {
		return line
	}
	croppedRows := min(visibleRows, metadata.Rows-hiddenRows)
	if hiddenRows == 0 && croppedRows == metadata.Rows {
		return line
	}
	sourceY := (metadata.HeightPx * hiddenRows) / metadata.Rows
	sourceEnd := ceilDiv(metadata.HeightPx*(hiddenRows+croppedRows), metadata.Rows)
	sourceHeight := max(1, min(metadata.HeightPx, sourceEnd)-sourceY)

	controlsStr := line[loc[2]:loc[3]] // submatch group 1
	var controls []string
	for control := range strings.SplitSeq(controlsStr, ",") {
		if !kittyCropControlPattern.MatchString(control) {
			controls = append(controls, control)
		}
	}
	controls = append(controls,
		"y="+strconv.Itoa(sourceY),
		"h="+strconv.Itoa(sourceHeight),
		"r="+strconv.Itoa(croppedRows),
	)
	return line[:loc[0]] + "\x1b_G" + strings.Join(controls, ",") + ";" + line[loc[1]:]
}

func ceilDiv(a, b int) int {
	if b == 0 {
		return 0
	}
	q := a / b
	if a%b != 0 {
		q++
	}
	return q
}

// kittyPrefix is the APC prefix that begins every Kitty graphics command.
const kittyPrefix = "\x1b_G"

// kittyMoreChunksPattern matches m=1 (more chunks follow) in a control string.
// Mirrors upstream /(?:^|,)m=1(?:,|$)/.
var kittyMoreChunksPattern = regexp.MustCompile(`(?:^|,)m=1(?:,|$)`)

// kittyPlacementControlKeys is the set of control keys copied into a
// placement-only command. Mirrors upstream KITTY_PLACEMENT_CONTROL_KEYS.
var kittyPlacementControlKeys = map[string]bool{
	"i": true, "p": true, "x": true, "y": true, "w": true, "h": true,
	"X": true, "Y": true, "c": true, "r": true, "C": true, "U": true,
	"z": true, "P": true, "Q": true, "H": true, "V": true,
}

// KittyImagePlacement is a placement-only command derived from a transmitted
// Kitty image line. Mirrors upstream KittyImagePlacement.
type KittyImagePlacement struct {
	ImageID                int
	TransmissionGeneration int
	TransmissionBytes      int
	EstimatedDecodedBytes  int
	Sequence               string
	ReplacementLine        string
}

// GetKittyImagePlacement builds a placement-only command for an image line
// emitted by RenderImage, so the alt-screen renderer can re-place an already
// transmitted image without re-uploading its data. Returns ok=false for a line
// with no Kitty command or no registered metadata. Mirrors upstream
// getKittyImagePlacement.
func GetKittyImagePlacement(line string) (KittyImagePlacement, bool) {
	loc := kittyGraphicPattern.FindStringSubmatchIndex(line)
	metadata, mok := getRegisteredKittyImageMetadata(line)
	if loc == nil || !mok {
		return KittyImagePlacement{}, false
	}
	matchIndex := loc[0]
	controlsStr := line[loc[2]:loc[3]]

	commandStart := matchIndex
	commandControls := controlsStr
	var transmissionEnd int
	for {
		rel := strings.Index(line[commandStart+len(kittyPrefix):], "\x1b\\")
		if rel == -1 {
			return KittyImagePlacement{}, false
		}
		terminator := commandStart + len(kittyPrefix) + rel
		transmissionEnd = terminator + 2
		if !kittyMoreChunksPattern.MatchString(commandControls) {
			break
		}
		commandStart = transmissionEnd
		if !strings.HasPrefix(line[commandStart:], kittyPrefix) {
			return KittyImagePlacement{}, false
		}
		relEnd := strings.IndexByte(line[commandStart+len(kittyPrefix):], ';')
		if relEnd == -1 {
			return KittyImagePlacement{}, false
		}
		controlsEnd := commandStart + len(kittyPrefix) + relEnd
		commandControls = line[commandStart+len(kittyPrefix) : controlsEnd]
	}

	var kept []string
	for control := range strings.SplitSeq(controlsStr, ",") {
		key := control
		if before, _, ok := strings.Cut(control, "="); ok {
			key = before
		}
		if kittyPlacementControlKeys[key] {
			kept = append(kept, control)
		}
	}
	sequence := "\x1b_Ga=p,q=2," + strings.Join(kept, ",") + "\x1b\\"
	return KittyImagePlacement{
		ImageID:                metadata.ImageID,
		TransmissionGeneration: metadata.transmissionGeneration,
		TransmissionBytes:      transmissionEnd - matchIndex,
		EstimatedDecodedBytes:  metadata.WidthPx * metadata.HeightPx * 4,
		Sequence:               sequence,
		ReplacementLine:        line[:matchIndex] + sequence + line[transmissionEnd:],
	}, true
}
