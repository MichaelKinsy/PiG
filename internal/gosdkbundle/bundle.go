// SPDX-License-Identifier: MIT

// Package gosdkbundle carries the Go extension SDK's sources so an installed pig can stage a buildable copy to disk: the Go analogue of extensions/sdk-py, sdk-rs and sdk-lisp, whose bundles are packages of the root module.
//
// The SDK itself is the nested module extensions/sdk, which the root module requires at a published version (docs/project/RELEASING.md): the root must not read anything of that module that the published version lacks, so the root's copy of the SDK's sources is not the module's own bundle (extensions/sdk/bundle.go, which stays what it was when the module was published) but this package's, generated from the module's files. See extensions/sdk/bundle.go for why a stager needs the sources and internal/sdkpack for the layout of the frame, which every SDK bundle shares.
//
// The bundle is one file, sdk.zst: the module declaration, the license and every non-test .go file of extensions/sdk as one zstd frame, in the layout internal/sdkpack documents. bundle_pack.go (go generate, automation/gen/packsdk) embeds the frame as Source and lists its files and their staging hash; coding/extension/pigsdk decodes it only when it stages the SDK. The module's bundle.go and bundle_embed.go are left out: the staged copy is a plain `package sdk` that does not carry an embed of itself. A named list silently shipped extensions a partial SDK when session_mirror.go was added: the file compiled into pig, was never staged, and every extension kept building against an SDK missing it. TestBundledFilesCoverTheModule fails when the list and the module's files disagree.
package gosdkbundle

import "slices"

//go:generate go run ../../automation/gen/packsdk -pkg gosdkbundle -root ../../extensions/sdk -skip _test.go -skip bundle.go -skip bundle_embed.go LICENSE go.mod *.go json/*.go json/LICENSE frontend/*.go kit/*.go internal/jsloop/*.go internal/kitwire/*.go tui/*.go tui/internal/*/*.go upgrade/*.go upgrade/driver/*.go

// BundledFiles lists the bundled files, relative to the module root, in a stable order. Paths use forward slashes; a stager recreates subdirectories. The returned slice is the caller's to modify.
func BundledFiles() []string { return slices.Clone(bundledFiles) }

// StagedHash is the hash a staged copy is named and marked by: what coding/extension/pigsdk computes from the bundled files' names, sizes and contents.
func StagedHash() string { return stagedHash }
