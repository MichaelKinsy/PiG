// This directory holds data only. Its own go.mod keeps it out of the root module zip that go install downloads; the files stay in the repository and tools and tests read them by path. Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/test/parity/interface-closure/specs

go 1.26.0
