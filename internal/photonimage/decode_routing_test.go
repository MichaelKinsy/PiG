// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Pi decodes the bytes of an image attachment, a clipboard image, a tool result image and a durable read with Photon
// (`PhotonImage.new_from_byteslice`). Go's JPEG and WebP decoders give other pixels, so no production code may hand
// those bytes to them: every decode goes through Decode. The stdlib decoders stay registered only for the lossless formats Decode falls back to.

// goDecoderPackages are the packages whose Decode and DecodeConfig read an image file.
var goDecoderPackages = []string{"image", "image/jpeg", "image/png", "image/gif", "golang.org/x/image/bmp", "golang.org/x/image/tiff", "golang.org/x/image/webp"}

// lossyDecoderPackages must not be registered by production code: with them registered a stray image.Decode would
// silently decode a JPEG or a WebP with Go's pixels instead of Photon's.
var lossyDecoderPackages = []string{"image/jpeg", "golang.org/x/image/webp"}

// decodeFallbackFile is the one production file that calls the standard library: Decode's fallback for the lossless formats.
const decodeFallbackFile = "internal/photonimage/decode.go"

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// productionGoFiles are the repository's .go files that are not tests, fixtures or the vendored upstream.
func productionGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
			files = append(files, file)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNoProductionCodeDecodesWithTheGoDecoders(t *testing.T) {
	root := repositoryRoot(t)
	fset := token.NewFileSet()
	checked := 0
	for _, file := range productionGoFiles(t, root) {
		relative := filepath.ToSlash(strings.TrimPrefix(file, root+string(filepath.Separator)))
		imports, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			continue // a file the Go tool chain rejects too
		}
		names := map[string]string{} // local name -> import path of the decoder packages the file imports
		for _, spec := range imports.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !slices.Contains(goDecoderPackages, importPath) {
				continue
			}
			if spec.Name != nil && spec.Name.Name == "_" {
				if slices.Contains(lossyDecoderPackages, importPath) {
					t.Errorf("%s: blank import of %s registers Go's lossy decoder; JPEG and WebP are decoded by photonimage.Decode", relative, importPath)
				}
				continue
			}
			local := path.Base(importPath)
			if spec.Name != nil {
				local = spec.Name.Name
			}
			names[local] = importPath
		}
		if len(names) == 0 || relative == decodeFallbackFile {
			continue
		}
		checked++
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Decode" && selector.Sel.Name != "DecodeConfig") {
				return true
			}
			if ident, ok := selector.X.(*ast.Ident); ok && ident.Obj == nil {
				if importPath, imported := names[ident.Name]; imported {
					t.Errorf("%s: %s.%s (%s) decodes with Go's decoder; use photonimage.Decode", fset.Position(call.Pos()), ident.Name, selector.Sel.Name, importPath)
				}
			}
			return true
		})
	}
	t.Logf("%d production files import a Go image decoder package", checked)
}

// Decode falls back to the standard library for the lossless formats, so it registers those decoders itself instead of relying on its callers.
func TestDecodeRegistersTheLosslessDecodersItFallsBackTo(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "decode.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var blank []string
	for _, spec := range parsed.Imports {
		if spec.Name != nil && spec.Name.Name == "_" {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			blank = append(blank, importPath)
		}
	}
	for _, want := range []string{"image/gif", "image/png", "golang.org/x/image/bmp"} {
		if !slices.Contains(blank, want) {
			t.Errorf("decode.go does not register %s (blank imports: %v)", want, blank)
		}
	}
	for _, forbidden := range lossyDecoderPackages {
		if slices.Contains(blank, forbidden) {
			t.Errorf("decode.go registers %s", forbidden)
		}
	}
}
