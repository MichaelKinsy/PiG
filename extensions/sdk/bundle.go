// SPDX-License-Identifier: MIT

package sdk

import "slices"

// Source carries this package's own module sources so an installed pig can
// stage a buildable copy of the SDK to disk.
//
// A clean, binary-only host has no pig source checkout to point a `replace`
// directive at, and this module (github.com/MichaelKinsy/PiG/extensions/sdk) is a
// nested module that is not published to a public proxy. Without a local copy,
// `go build` cannot resolve the SDK, so `pig install`/`-e` and packed runtime
// cells fail to build any out-of-tree Go extension. Staging these embedded
// files (see coding/extension/pigsdk) gives every such build a local SDK
// module that always matches the running binary.
//
// bundle.go and bundle_pack.go are intentionally excluded from the bundle: the
// staged copy is a minimal, dependency-free `package sdk` that does not carry
// the embed itself.
//
// The bundle is one file, sdk.zst: every non-test .go file, the module
// declaration and the license as one zstd frame, in the layout of
// internal/sdkpack. The sources are most of the weight this package adds to every
// binary that stages them, and source text is a fifth of its size compressed;
// one solid frame compresses a quarter better than a gzip of each file. bundle_pack.go
// (go generate, automation/gen/packsdk) embeds the frame as Source and lists its
// files and their staging hash, so a start that stages nothing decodes nothing. This
// module has no dependencies, so it does not decode the frame: coding/extension/pigsdk
// does, when it stages the SDK, and writes each file under its name in BundledFiles.
// A named list silently shipped extensions a partial SDK when session_mirror.go was
// added: the file compiled into pig, was never staged, and every extension kept
// building against an SDK missing it. TestBundledFilesCoversModule fails when the
// list and the source tree disagree, and its message names the command that
// regenerates it.
//
//go:generate go run ../../automation/gen/packsdk -pkg sdk -skip _test.go -skip bundle.go -skip bundle_pack.go LICENSE go.mod *.go json/*.go json/LICENSE frontend/*.go kit/*.go internal/jsloop/*.go internal/kitwire/*.go tui/*.go tui/internal/*/*.go upgrade/*.go upgrade/driver/*.go

// BundledFiles lists the bundled filenames, relative to the module root, in a
// stable order. It is the canonical set a stager must write to produce a
// compilable SDK module; the returned slice is the caller's to modify.
//
// bundle.go and _test.go files are excluded: the staged module is a plain
// package sdk with no embed of its own and no tests.
func BundledFiles() []string { return slices.Clone(bundledFiles) }

// StagedHash is the hash a staged copy is named and marked by: what
// coding/extension/pigsdk computes from the bundled files' names, sizes and
// contents.
func StagedHash() string { return stagedHash }
