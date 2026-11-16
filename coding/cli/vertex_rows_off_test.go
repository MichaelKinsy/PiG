//go:build pig_strip_google_vertex

package cli

// whenVertex returns the table rows of the Google Vertex AI provider: a Piglet that strips it has no such provider to test.
func whenVertex[T any](...T) []T { return nil }
