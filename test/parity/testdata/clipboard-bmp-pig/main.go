package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func bmp() []byte {
	data := make([]byte, 58)
	copy(data, "BM")
	for offset, value := range map[int]uint32{2: 58, 10: 54, 14: 40, 18: 1, 22: 1, 34: 4} {
		binary.LittleEndian.PutUint32(data[offset:], value)
	}
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 24)
	data[56] = 255
	return data
}

func main() {
	if filepath.Base(os.Args[0]) == "wl-paste" {
		data := bmp()
		if slices.Contains(os.Args, "--list-types") {
			data = []byte("image/bmp\n")
		}
		if _, err := os.Stdout.Write(data); err != nil {
			panic(err)
		}
		return
	}
	root := os.Getenv("PARITY_CLIPBOARD_DIR")
	if root == "" {
		panic("PARITY_CLIPBOARD_DIR is required")
	}
	dir, err := os.MkdirTemp(root, "clipboard-go-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	if err = os.Symlink(exe, filepath.Join(dir, "wl-paste")); err != nil {
		panic(err)
	}
	if err = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		panic(err)
	}
	if err = os.Setenv("WAYLAND_DISPLAY", "wayland-0"); err != nil {
		panic(err)
	}
	data, mime, err := codingagent.ReadClipboardImageContext(context.Background())
	if err != nil {
		panic(err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	result := struct {
		MIME      string   `json:"mimeType"`
		Signature string   `json:"signature"`
		Width     int      `json:"width"`
		Height    int      `json:"height"`
		RGBA      []uint32 `json:"rgba"`
	}{mime, hex.EncodeToString(data[:4]), img.Bounds().Dx(), img.Bounds().Dy(), []uint32{r >> 8, g >> 8, b >> 8, a >> 8}}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
