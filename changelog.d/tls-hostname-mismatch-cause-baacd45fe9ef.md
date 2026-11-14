### Fixed

- A request that fails because the server's TLS certificate does not name the host now shows the reason after `Connection error.` (`Hostname/IP does not match certificate's altnames: ...`), as Pi does, for the Anthropic, OpenAI completions and OpenAI responses APIs. <!-- fix: baacd45fe9ef --> <!-- cause: nodeConnectionFailure mapped only dial, socket and EOF failures to Node's cause text; the TLS name mismatch had no message -->
