### Changed

- Updated Go module dependencies (`golang.org/x/oauth2` 0.37.0, `github.com/Microsoft/go-winio`, `go.yaml.in/yaml/v3`, `golang.org/x/sys`). `cloud.google.com/go/compute` stays pinned at v1.24.0 so the `cloud.google.com/go/compute/metadata` import that `oauth2/google` needs resolves to the split-out module and not to a second module that still ships it.
