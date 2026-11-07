// SPDX-License-Identifier: MIT

// Package lispsdk carries the Common Lisp extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk-py/bundle.go for the rationale;
// this is the Lisp analogue. Staging writes these files under
// <config-root>/state/pigsdk/sdk-lisp-<hash16>, a directory per build named for their content
// hash, so Lisp cells resolve the SDK on a clean host (findLispSDKRoot's
// staged-source strategy). The tests and the README stay out of the staged copy.
//
// The sources travel as one zstd frame (sdk.zst, the layout internal/sdkpack documents): text compresses to
// a quarter of its size, and the stock pig binary carries the SDK whether or not a Lisp extension is ever installed. Pig learns the file list and a
// staging hash without decompressing; coding/extension/pigsdk decodes the frame when it stages the SDK.
package lispsdk

import "slices"

//go:generate go run ../../automation/gen/packsdk -pkg lispsdk -skip bundle.go -skip bundle_pack.go LICENSE pig-sdk.asd src/*.lisp

// BundledFiles lists the bundled files, relative to the SDK root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories. The
// returned slice is the caller's to modify.
func BundledFiles() []string { return slices.Clone(bundledFiles) }

// StagedHash is the hash a staged copy is named and marked by: what coding/extension/pigsdk computes from the bundled files' names, sizes and contents.
func StagedHash() string { return stagedHash }
