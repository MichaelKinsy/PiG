// Ports apps/p2-mac/Sources/P2Mac/main.swift
// Entry point: an AppKit app with AppDelegate as its delegate.
import AppKit

MainActor.assumeIsolated {
	let delegate = AppDelegate()
	NSApplication.shared.delegate = delegate
	NSApplication.shared.run()
}
