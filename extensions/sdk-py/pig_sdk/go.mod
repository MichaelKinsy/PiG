// This directory holds no Go package: it is the Python SDK's package sources. Its own go.mod keeps them out of the root module zip that go install downloads; pig carries them in ../sdk.zst (go generate ./extensions/sdk-py packs pig_sdk/*.py and pig_sdk/tui/*.py), and the files stay in the repository, where Python, tests, generators and drift gates read them by path. Never go:embed from here. automation/release/modulesize gates the root module zip size.
module github.com/MichaelKinsy/PiG/extensions/sdk-py/pig_sdk

go 1.26.0
