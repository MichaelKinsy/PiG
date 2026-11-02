### Fixed

- `pig status` and `pig status --json` no longer print the credentials of a configured Package source. A git URL with a user and token, or a secret query parameter, appeared in the Package errors and the resource items; status now names such a URL as Pi's bug report does, without them, and a settings syntax error no longer quotes the text of the file around it. <!-- fix: 316eac6c4b, cf3c76d945 -->
