//go:build !pig_strip_google_vertex

package ai

import "testing"

// BenchmarkGoogleVertexHookedRequest is in its own file: a build with pig_strip_google_vertex has no Vertex row in hookedProviders to measure.
func BenchmarkGoogleVertexHookedRequest(b *testing.B) { benchmarkHookedRequest(b, "vertex") }
