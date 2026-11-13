package codingagent

// pi: packages/coding-agent/src/cli/file-processor.ts

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi :31-85 awaits one argument at a time: text blocks and image attachments
// land in argument order across several arguments of mixed kinds.
func TestProcessCLIFileArgumentsKeepsArgumentOrderAcrossArguments(t *testing.T) {
	dir := t.TempDir()
	red := makePNGImage(t, 8, 8, color.RGBA{255, 0, 0, 255})
	blue := makeJPEGImage(t, 8, 8, color.RGBA{0, 0, 255, 255})
	files := map[string][]byte{"a.txt": []byte("alpha"), "first.png": red, "b.txt": []byte("beta"), "second.jpg": blue}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ProcessCLIFileArguments([]string{"a.txt", "first.png", "b.txt", "second.jpg"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + filepath.Join(dir, "a.txt") + `">` + "\nalpha\n</file>\n" +
		`<file name="` + filepath.Join(dir, "first.png") + `"></file>` + "\n" +
		`<file name="` + filepath.Join(dir, "b.txt") + `">` + "\nbeta\n</file>\n" +
		`<file name="` + filepath.Join(dir, "second.jpg") + `"></file>` + "\n"
	if got.Text != want {
		t.Fatalf("Text = %q, want %q", got.Text, want)
	}
	if len(got.Images) != 2 {
		t.Fatalf("Images = %d, want 2", len(got.Images))
	}
	wantMimes := []string{"image/png", "image/jpeg"}
	for i, image := range got.Images {
		if image.MimeType != wantMimes[i] {
			t.Fatalf("Images[%d].MimeType = %q, want %q", i, image.MimeType, wantMimes[i])
		}
	}
	if raw, _ := base64.StdEncoding.DecodeString(got.Images[0].Data); len(raw) == 0 || bytes.Equal(raw, blue) {
		t.Fatal("first attachment is not the first image argument")
	}
}

// Pi :36 fails on the first missing argument (process.exit(1)): the result is
// an error naming that file and no later argument is processed or returned.
func TestProcessCLIFileArgumentsFailsFastOnFirstMissingArgument(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ProcessCLIFileArguments([]string{"a.txt", "missing-one.txt", "b.txt", "missing-two.txt"}, dir)
	if err == nil {
		t.Fatalf("err = nil, result %+v; want the missing-file error", got)
	}
	if !strings.Contains(err.Error(), "missing-one.txt") || strings.Contains(err.Error(), "missing-two.txt") {
		t.Fatalf("err = %v, want it to name only the first missing file", err)
	}
	if got.Text != "" || len(got.Images) != 0 {
		t.Fatalf("result = %+v, want empty on failure", got)
	}
}
