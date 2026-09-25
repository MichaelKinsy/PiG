package main

import "testing"

func TestStartupTraceDisabledHasNoExtensionHook(t *testing.T) {
	disabled := startupTrace{}
	var installed func(string)
	installStartupTrace(&disabled, func(mark func(string)) { installed = mark })
	if installed != nil {
		t.Fatal("disabled startup tracing installed an extension hook")
	}

	enabled := startupTrace{enabled: true}
	installStartupTrace(&enabled, func(mark func(string)) { installed = mark })
	if installed == nil {
		t.Fatal("enabled startup tracing omitted its extension hook")
	}
}

func BenchmarkStartupTraceDisabledExtensionHook(b *testing.B) {
	disabled := startupTrace{}
	var installed func(string)
	installStartupTrace(&disabled, func(mark func(string)) { installed = mark })
	b.ReportAllocs()
	for b.Loop() {
		if installed != nil {
			installed("extension.example.discover-start")
		}
	}
}
