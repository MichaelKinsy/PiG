// SPDX-License-Identifier: MIT

// Package lispsdk carries the Common Lisp extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk-py/bundle.go for the rationale;
// this is the Lisp analogue. Staging writes these files under
// <config-root>/state/pigsdk/sdk-lisp-<hash16>, a directory per build named for their
// content hash, so Lisp cells resolve the SDK on a clean host (findLispSDKRoot's
// staged-source strategy). The tests and the README stay out of the staged copy.
package lispsdk

import (
	"embed"
	"io/fs"
	"slices"
)

//go:embed LICENSE pig-sdk.asd src/*.lisp
var Source embed.FS

// BundledFiles lists the embedded files, relative to the SDK root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories.
func BundledFiles() []string {
	files, err := fs.Glob(Source, "src/*.lisp")
	if err != nil {
		panic(err)
	}
	files = append(files, "LICENSE", "pig-sdk.asd")
	slices.Sort(files)
	return files
}
