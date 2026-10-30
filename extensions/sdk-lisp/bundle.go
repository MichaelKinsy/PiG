// SPDX-License-Identifier: MIT

// Package lispsdk carries the Common Lisp extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk-py/bundle.go for the rationale;
// this is the Lisp analogue. Staging writes these files under
// <config-root>/state/pigsdk/sdk-lisp-<hash16>, a directory per build named for their
// content hash, so Lisp cells resolve the SDK on a clean host (findLispSDKRoot's
// staged-source strategy). The tests and the README stay out of the staged copy.
//
// The sources travel as one gzip-compressed tar (sdk.tgz): text compresses to a quarter of its size, and the
// stock pig binary carries the SDK whether or not a Lisp extension is ever installed. Pig learns the file list and a
// staging hash without decompressing; the archive is decoded the first time a file is read.
package lispsdk

//go:generate go run ../../automation/gen/pack-lisp-sdk.go

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"io"
	"io/fs"
	"slices"
	"sync"
	"time"
)

//go:embed sdk.tgz
var archive []byte

// BundledFiles lists the bundled files, relative to the SDK root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories.
func BundledFiles() []string { return slices.Clip(bundledFiles) }

// StagedHash is the hash a staged copy is named and marked by: what pigsdk computes from the bundled files' names, sizes and contents.
func StagedHash() string { return stagedHash }

// Source serves the bundled files, decoding the archive when the first is opened.
var Source fs.ReadFileFS = packed{}

var unpacked = sync.OnceValues(func() (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(bundledFiles))
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[header.Name] = data
	}
})

type packed struct{}

func (packed) ReadFile(name string) ([]byte, error) {
	files, err := unpacked()
	if err != nil {
		return nil, err
	}
	data, ok := files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return data, nil
}

func (p packed) Open(name string) (fs.File, error) {
	data, err := p.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &packedFile{Reader: bytes.NewReader(data), name: name, size: int64(len(data))}, nil
}

type packedFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *packedFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *packedFile) Close() error               { return nil }
func (f *packedFile) Name() string               { return f.name }
func (f *packedFile) Size() int64                { return f.size }
func (f *packedFile) Mode() fs.FileMode          { return 0o444 }
func (f *packedFile) ModTime() time.Time         { return time.Time{} }
func (f *packedFile) IsDir() bool                { return false }
func (f *packedFile) Sys() any                   { return nil }
