// This directory holds data only. Its own go.mod keeps it out of the root module zip that go install downloads; the npm packs stay in the repository and tools and tests read them by path (file: dependencies, automation/pin, internal/pioracle). Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/third_party/pi-packs

go 1.26.0
