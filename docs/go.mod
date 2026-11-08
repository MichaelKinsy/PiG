// This directory holds data only. Its own go.mod keeps it out of the root module zip that go install downloads; the files stay in the repository and tests, generators and drift gates read them by path. Never go:embed from here (internal/pigdocs embeds its own generated copy of the docs bundle). automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/docs

go 1.26.0
