// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package frontend is the contract between PiG's interactive mode and a
// Piglet frontend member that draws it in place of the ANSI renderer (D91).
//
// A frontend member is a Go package compiled into a Piglet Binary. It is not
// part of the extension API: it has no wire protocol and no subprocess
// realization, because it owns the terminal's output and reads terminal input
// on the interactive owner loop.
//
// PiG keeps building Pi's component tree. Each frame, it reports what changed
// as retained-tree ops keyed by stable ids: transcript entries in [RegionMain]
// and the input dock in [RegionDock]. Tool calls arrive as [ToolCard] nodes; every other
// component arrives as [Lines], the ANSI lines the component rendered.
package frontend

import (
	"io"
	"time"
)

// Frontend opens a frontend session for one interactive run.
type Frontend interface {
	// Open runs before the first paint, before PiG reads terminal input. A nil
	// Session with a nil error means the frontend does not apply here, and PiG
	// paints with its ANSI renderer.
	Open(env Env) (Session, error)
}

// Env describes the terminal a session draws on.
type Env struct {
	// Out writes to the terminal. Write to it only from Open and the Session
	// methods, which run where PiG's renderer writes.
	Out io.Writer
	// Columns is the terminal width in cells.
	Columns int
	// Getenv reads the process environment.
	Getenv func(key string) string
	// AppName and Version identify the running application.
	AppName, Version string
	// Fallback asks PiG to stop the session and continue with its ANSI
	// renderer. It may be called from any goroutine; PiG closes the session
	// on its owner loop and repaints the whole tree.
	Fallback func(reason string)
}

// Session draws one interactive run. PiG calls every method on its owner
// loop, never concurrently; a method must not block on terminal input.
type Session interface {
	// InputReady reports that PiG's input loop is running: from now on,
	// answers to what the session wrote in Open reach HandleInput without a
	// startup delay. A session that waits for an answer starts its timeout
	// here. PiG calls it once, after the first Apply.
	InputReady()
	// Columns reports how many terminal columns the session lays out in the
	// main and dock regions. PiG renders each region's lines at that width,
	// capped at the terminal's; zero or less means the terminal's width. PiG
	// reads it for every frame and after HandleInput takes a sequence, so a
	// change from a resize event repaints.
	Columns() (main, dock int)
	// Apply draws one frame.
	Apply(frame Frame) error
	// HandleInput is offered every terminal input sequence before key
	// handling. It reports whether the sequence belonged to the frontend.
	HandleInput(data string) bool
	// Close ends the session. PiG discards the terminal input that arrives
	// after Close, so answers still in flight never reach the shell.
	Close() error
}

// Frame is the change since the previous frame. The first frame inserts the
// whole tree.
type Frame struct {
	Ops []Op
}

// Region is where a node sits.
type Region string

const (
	// RegionMain is the transcript: header, loaded resources, messages and
	// tools.
	RegionMain Region = "main"
	// RegionDock is the input area under the transcript: pending messages,
	// status, widgets, the editor and the footer.
	RegionDock Region = "dock"
)

// OpKind is the kind of change an op makes.
type OpKind string

const (
	// Insert adds Node with id ID at Index in Region.
	Insert OpKind = "insert"
	// Update replaces the node with id ID. Index is its current position.
	Update OpKind = "update"
	// Remove deletes the node with id ID.
	Remove OpKind = "remove"
)

// Op is one change to the retained tree. Ops apply in order; Index counts the
// region's nodes after every earlier op of the frame.
type Op struct {
	Kind   OpKind
	Region Region
	ID     string
	Index  int
	// Node is nil for Remove.
	Node Node
}

// Node is a closed set of node kinds: [Lines] and [ToolCard].
type Node interface{ frontendNode() }

// Lines is a component drawn as ANSI lines.
type Lines struct {
	Lines []string
}

// ToolStatus is a tool call's lifecycle state.
type ToolStatus string

const (
	// ToolPending is a call whose arguments are still streaming or that has
	// not started executing.
	ToolPending ToolStatus = "pending"
	// ToolRunning is a call that is executing.
	ToolRunning ToolStatus = "running"
	// ToolDone is a call that finished.
	ToolDone ToolStatus = "done"
	// ToolError is a call that failed or was aborted.
	ToolError ToolStatus = "error"
)

// ToolCard is one tool call.
type ToolCard struct {
	// Name is the tool name.
	Name string
	// Arguments is the call's argument object, nil while its JSON is still
	// incomplete. It is shared with PiG and must not be modified.
	Arguments map[string]any
	// Header is PiG's one-line call header as plain text, such as
	// "$ ls -la" or "read src/main.go".
	Header string
	Status ToolStatus
	// Output is the result's plain text so far.
	Output string
	// Elapsed is the finished call's duration, or zero.
	Elapsed time.Duration
	// Expanded reports whether the user expanded the tool output.
	Expanded bool
	// Result holds the result as the tool's definition renders it, for a
	// tool drawn through registered renderers; nil otherwise. The call's
	// rendering is not sent: a frontend draws the call from Arguments.
	Result []string
}

func (Lines) frontendNode()    {}
func (ToolCard) frontendNode() {}
