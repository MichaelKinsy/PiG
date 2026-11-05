package platform

// Common keeps the fixture package loadable on every host, so the linux-only
// file is the only thing the scan can miss.
func Common() string { return "platform" }
