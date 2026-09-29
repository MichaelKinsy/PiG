package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareInitialMessageDefersImageResizeUntilModelSelection(t *testing.T) {
	// Pi main.ts:222 passes autoResizeImages:false so before_agent_start sees the original image.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2400, 100))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "large.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	text, images, _, err := prepareInitialMessage(filepath.Dir(path), []string{"take a look"}, []string{path}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].Data != base64.StdEncoding.EncodeToString(encoded.Bytes()) {
		t.Fatal("CLI resized the image before request-model selection")
	}
	if strings.Contains(text, "displayed at") {
		t.Fatalf("CLI inserted a premature resize note: %q", text)
	}
}

func BenchmarkPrepareInitialImageWithoutPrematureResize(b *testing.B) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2400, 100))); err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	path := filepath.Join(dir, "large.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, _, err := prepareInitialMessage(dir, []string{"take a look"}, []string{path}, ""); err != nil {
			b.Fatal(err)
		}
	}
}
