<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# PiG project context

This file defines the terms that public PiG documentation and implementation use.

## Pi

Pi is the upstream TypeScript project at <https://github.com/earendil-works/pi>. Pi is the reference implementation for parity-bound behavior.

## PiG

PiG is the Go implementation in this repository. The executable is `pig`. PiG follows the exact Pi release pinned in `coding/pigversion/pigversion.go`.

## Stock PiG

Stock PiG is the product-neutral binary built from this repository. It contains
Pi-compatible behavior, generic extension and Piglet infrastructure, and the
reference documentation required to use those APIs. It activates no optional
product Resource and no implicit Piglet composition.

## Resource

A Resource contributes one extension, skill, prompt, hook, MCP definition, theme, compatible agent declaration, context input, or agent environment.

## Package

A Package distributes a versioned collection of Resources. Installing a Package makes its Resources available through normal discovery. A Package does not own or activate a Piglet.

## Plugin

A Plugin packages capabilities for more than one compatible agent harness through an Agent Plugin or another supported vendor format.

## Piglet

A Piglet defines one named agent. It selects and scopes Resources and declares defaults and environment requirements.

## Piglet release

A Piglet release pins the exact Resource closure and executable component plan for a Piglet.

## Piglet Binary

A Piglet Binary is a target-native PiG executable built for one immutable Piglet composition. It can contain fused Go extensions and prebuilt Go or Rust extension cells. External services, configuration, secrets, and interpreted runtimes remain external requirements unless the release states otherwise.

## PiG Standard

PiG Standard is the optional curated Piglet at
`piglets/standard/pig-standard.yaml`. It is not Stock PiG. A user must select
or build it explicitly. A Package can distribute its Resources but cannot own
or activate the Piglet.

## Pig Porter

Pig Porter is the local interactive and headless workbench for upstream-first parity work. It is a Piglet Resource in this repository. It does not define PiG product behavior.

## Divergence

A divergence is an intentional user-visible or interoperability difference from the pinned Pi behavior. `DIVERGENCES.md` records each approved divergence. Language mechanics and implementation details that do not change observable behavior are not divergences.

## Evidence

Evidence is a reproducible artifact that supports a project claim. Scanner output is evidence input. It is not a validated inventory, license conclusion, vulnerability disposition, or compliance approval by itself.

## Transcript

A Transcript is normalized, ordered model context. It carries typed system, user, assistant, and tool-result entries in their original order.

## Provider

A Provider consumes a normalized Transcript and produces one Event Stream.

## Event Stream

An Event Stream yields ordered model events and one terminal assistant result. The caller context owns cancellation.

## Model Runtime

A Model Runtime owns mode-independent model lookup, authentication, completion, and streaming. TUI, print, JSON, and RPC use the same Model Runtime.

## Main Screen

The Main Screen renders transcript and active input into the visible terminal region. Ordinary input changes update only the required visible rows and preserve terminal scrollback unless Pi requires a recovery redraw.

## Extension Host

The Extension Host realizes extension Resources as isolated, packed, or fused components. External components retain their own declared protocol and lifecycle.
