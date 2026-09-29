// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/tui"
)

const (
	escByte             = "\x1b"
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// StdinBuffer is the shared UTF-16 framing state used by terminal owners.
type StdinBuffer = tui.StdinBuffer
type StdinBufferOptions = tui.StdinBufferOptions

func NewStdinBuffer(options StdinBufferOptions) *StdinBuffer { return tui.NewStdinBuffer(options) }

// dropKeyReleases filters delivery to the focused component while raw listeners retain the complete input stream.
func dropKeyReleases(component tui.Component, chunks []string) []string {
	return slices.DeleteFunc(chunks, func(chunk string) bool { return !tui.ShouldDeliverKey(component, chunk) })
}

func dispatchModalInput(component tui.Component, chunks []string, handleInput func(string), done func() bool) {
	for _, chunk := range dropKeyReleases(component, chunks) {
		if chunk == "" {
			continue
		}
		handleInput(chunk)
		if done() {
			return
		}
	}
}

func drainModalInput(component tui.Component, ch <-chan []byte, handle func(string) bool) {
	for {
		select {
		case more := <-ch:
			if slices.ContainsFunc(dropKeyReleases(component, []string{string(more)}), handle) {
				return
			}
		default:
			return
		}
	}
}
