// This directory holds no Go package: it is the Rust SDK's crate sources. Its own go.mod keeps them out of the root module zip that go install downloads; pig carries them in ../sdk.zst (go generate ./extensions/sdk-rs packs src without its unit-test modules and without this file), and the files stay in the repository, where cargo, tests, generators and drift gates read them by path. Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/extensions/sdk-rs/src

go 1.26.0
