// This directory holds data only: the pages of the agent docs bundle. Its own go.mod keeps them out of the root module zip that go install downloads; pig carries them in ../pages.zst (go generate ./internal/pigdocs packs content/*.md), and the files stay in the repository, where tests, generators and drift gates read them by path. Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/internal/pigdocs/content

go 1.26.0
