// This directory holds data only. Its own go.mod keeps it out of the root module zip that go install downloads; the files stay in the repository and tests, generators and vendoring scripts read them by path. The module embeds runtime-node.pack, generated from this tree by go generate. Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/runtime-node

go 1.26.0
