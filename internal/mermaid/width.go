// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "github.com/rivo/uniseg"

// Display width, measured in grapheme clusters, ported from grok-mermaid
// width.ts. A cluster is the unit of both measuring and painting, so a box is
// sized for exactly what gets drawn. Clustering comes from uniseg (UAX #29,
// matching Intl.Segmenter); per-code-point widths come from widthRuns
// (generated from the unicode-width crate), not go-runewidth.

const vs16 = 0xfe0f

func isRegionalIndicator(cp rune) bool { return cp >= 0x1f1e6 && cp <= 0x1f1ff }

// codePointWidth returns the width of one code point via binary search over the
// generated runs; the table covers the whole code point space (default 1).
func codePointWidth(cp rune) int {
	lo, hi := 0, len(widthRuns)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		run := widthRuns[mid]
		switch {
		case cp < run[0]:
			hi = mid - 1
		case cp > run[1]:
			lo = mid + 1
		default:
			return int(run[2])
		}
	}
	return 1
}

// clusterWidth returns the columns one grapheme cluster occupies. The widest
// code point wins (base + combining marks measures as the base); a VS16 emoji
// presentation selector or a regional-indicator pair (flag) forces two columns.
func clusterWidth(cluster string) int {
	w := 0
	hasVS16 := false
	regional := 0
	for _, ch := range cluster {
		if ch == vs16 {
			hasVS16 = true
		}
		if isRegionalIndicator(ch) {
			regional++
		}
		if cw := codePointWidth(ch); cw > w {
			w = cw
		}
	}
	if hasVS16 || regional >= 2 {
		return 2
	}
	return w
}

// stringWidth returns the display columns of a string, summed over grapheme
// clusters (uniseg segmentation, grok's width table).
func stringWidth(s string) int {
	w := 0
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		w += clusterWidth(gr.Str())
	}
	return w
}

// measuredCluster pairs a cluster with its display width.
type measuredCluster struct {
	cluster string
	width   int
}

func measured(s string) []measuredCluster {
	var out []measuredCluster
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		seg := gr.Str()
		out = append(out, measuredCluster{cluster: seg, width: clusterWidth(seg)})
	}
	return out
}
