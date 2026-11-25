// Standalone CI tool module. Its own go.mod keeps it out of the root module zip that `go install` downloads (automation/release/modulesize).
module github.com/MichaelKinsy/PiG/automation/ci/gocacheprog

go 1.26.0

require go.yaml.in/yaml/v3 v3.0.6
