### Changed

- The root module zip that `go install` downloads is 131.5 MB instead of 134.2 MB: `changelog.d` joins the data-only directories with their own `go.mod` (the changelog tools accept that one file there).
