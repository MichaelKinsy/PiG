package tui

import (
	"fmt"
	"strings"
	"testing"
)

func resetKittyRegistry(t *testing.T) {
	t.Helper()
	kittyMetadataMu.Lock()
	kittyImageMetadata = map[int]registeredKittyImageMetadata{}
	kittyImageMetadataOrder = nil
	kittyTransmissionGeneration = 0
	kittyMetadataMu.Unlock()
}

// kittyLine builds a minimal Kitty APC line carrying image id and payload.
func kittyLine(imageID int) string {
	return fmt.Sprintf("\x1b_Ga=T,f=100,i=%d,c=4,r=3;AAAA\x1b\\", imageID)
}

func TestGetKittyImageMetadataUnregisteredIsNil(t *testing.T) {
	resetKittyRegistry(t)
	if GetKittyImageMetadata(kittyLine(42)) != nil {
		t.Error("unregistered image should return nil metadata")
	}
	if GetKittyImageMetadata("plain text, no image") != nil {
		t.Error("non-image line should return nil metadata")
	}
}

func TestRegisterAndGetKittyImageMetadata(t *testing.T) {
	resetKittyRegistry(t)
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 42, Columns: 4, Rows: 3, WidthPx: 40, HeightPx: 60})
	got := GetKittyImageMetadata(kittyLine(42))
	if got == nil {
		t.Fatal("registered image should return metadata")
	}
	want := KittyImageMetadata{ImageID: 42, Columns: 4, Rows: 3, WidthPx: 40, HeightPx: 60}
	if *got != want {
		t.Errorf("metadata = %+v, want %+v", *got, want)
	}
}

func TestCropKittyImageLinePassthroughWhenUnregistered(t *testing.T) {
	resetKittyRegistry(t)
	line := kittyLine(7)
	if got := CropKittyImageLine(line, 0, 1); got != line {
		t.Errorf("unregistered crop should return line unchanged")
	}
}

func TestCropKittyImageLineRewritesControls(t *testing.T) {
	resetKittyRegistry(t)
	// rows=3, heightPx=60 -> each row is 20px.
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 9, Columns: 4, Rows: 3, WidthPx: 40, HeightPx: 60})
	line := kittyLine(9)
	// Hide the top row (hiddenRows=1), show 2 rows.
	got := CropKittyImageLine(line, 1, 2)
	if got == line {
		t.Fatal("crop should have rewritten the line")
	}
	// sourceY = floor(60*1/3) = 20; sourceEnd = ceil(60*3/3)=60; h = 60-20 = 40; r = 2.
	for _, want := range []string{"y=20", "h=40", "r=2"} {
		if !strings.Contains(got, want) {
			t.Errorf("cropped line missing %q: %q", want, got)
		}
	}
	// The original geometry controls that crop overrides must be gone/replaced;
	// r=3 (original) must not remain.
	if strings.Contains(got, "r=3") {
		t.Errorf("cropped line still has original r=3: %q", got)
	}
	// Payload after the ';' is preserved.
	if !strings.Contains(got, ";AAAA") {
		t.Errorf("cropped line dropped payload: %q", got)
	}
}

func TestCropKittyImageLineNoOpWhenFullyVisible(t *testing.T) {
	resetKittyRegistry(t)
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 5, Columns: 4, Rows: 3, WidthPx: 40, HeightPx: 60})
	line := kittyLine(5)
	// hiddenRows=0, visibleRows>=rows -> croppedRows==rows -> no-op.
	if got := CropKittyImageLine(line, 0, 3); got != line {
		t.Errorf("full-image crop should be a no-op, got %q", got)
	}
	// Out-of-range hiddenRows -> unchanged.
	if got := CropKittyImageLine(line, 3, 1); got != line {
		t.Errorf("hiddenRows>=rows should be a no-op, got %q", got)
	}
}

func TestRegisterKittyImageMetadataEvictsOldestPastCap(t *testing.T) {
	resetKittyRegistry(t)
	for id := 1; id <= kittyImageMetadataCap+5; id++ {
		RegisterKittyImageMetadata(KittyImageMetadata{ImageID: id, Columns: 1, Rows: 1, WidthPx: 1, HeightPx: 1})
	}
	kittyMetadataMu.Lock()
	size := len(kittyImageMetadata)
	_, oldestPresent := kittyImageMetadata[1]
	_, newestPresent := kittyImageMetadata[kittyImageMetadataCap+5]
	kittyMetadataMu.Unlock()
	if size != kittyImageMetadataCap {
		t.Errorf("registry size = %d, want %d (capped)", size, kittyImageMetadataCap)
	}
	if oldestPresent {
		t.Error("oldest entry (id=1) should have been evicted")
	}
	if !newestPresent {
		t.Error("newest entry should be present")
	}
}

func TestDeleteAllKittyPlacements(t *testing.T) {
	if got := DeleteAllKittyPlacements(); got != "\x1b_Ga=d,d=a,q=2\x1b\\" {
		t.Errorf("DeleteAllKittyPlacements() = %q", got)
	}
}

func TestGetKittyImagePlacement(t *testing.T) {
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 5, Columns: 10, Rows: 3, WidthPx: 100, HeightPx: 50})
	line := "\x1b_Gi=5,a=T,w=100,h=50,z=1;BASE64DATA\x1b\\"
	p, ok := GetKittyImagePlacement(line)
	if !ok {
		t.Fatal("expected a placement for a registered image line")
	}
	// Only placement-control keys survive; a=T is dropped (not in the set).
	wantSeq := "\x1b_Ga=p,q=2,i=5,w=100,h=50,z=1\x1b\\"
	if p.Sequence != wantSeq {
		t.Errorf("sequence = %q, want %q", p.Sequence, wantSeq)
	}
	if p.ImageID != 5 {
		t.Errorf("imageId = %d, want 5", p.ImageID)
	}
	if p.EstimatedDecodedBytes != 100*50*4 {
		t.Errorf("estimatedDecodedBytes = %d, want %d", p.EstimatedDecodedBytes, 100*50*4)
	}
	if p.TransmissionBytes != len(line) {
		t.Errorf("transmissionBytes = %d, want %d", p.TransmissionBytes, len(line))
	}
	if p.ReplacementLine != wantSeq {
		t.Errorf("replacementLine = %q, want %q", p.ReplacementLine, wantSeq)
	}
	// A line with no Kitty command yields no placement (deterministic: does not
	// depend on the shared registry, which other tests populate).
	if _, ok := GetKittyImagePlacement("plain text, no kitty"); ok {
		t.Error("non-kitty line should have no placement")
	}
}

func TestIsKeyRelease(t *testing.T) {
	if !IsKeyRelease(":3u") {
		t.Error(":3u should be a release")
	}
	if !IsKeyRelease("\x1b[97;1:3u") {
		t.Error("full release sequence should be detected")
	}
	// Bracketed paste content is never a release, even with ":3" patterns.
	if IsKeyRelease("\x1b[200~90:62:3F:A5\x1b[201~") {
		t.Error("bracketed paste must not count as release")
	}
	if IsKeyRelease("hello") {
		t.Error("plain text is not a release")
	}
}
