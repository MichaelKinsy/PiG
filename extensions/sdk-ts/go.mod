// This directory holds no Go package: it is the TypeScript declarations package for extension authors (index.d.ts, the pinned Pi declarations pi-pin.d.ts, its scripts and the frozen published declarations). Its own go.mod keeps it out of the root module zip that go install downloads; the files stay in the repository and npm, tests, generators and drift gates read them by path. Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/extensions/sdk-ts

go 1.26.0
