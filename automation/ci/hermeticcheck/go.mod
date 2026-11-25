// Standalone CI tool module. Its own go.mod keeps it out of the root module zip that `go install` downloads (automation/release/modulesize).
module github.com/MichaelKinsy/PiG/automation/ci/hermeticcheck

go 1.26.0
