### Added

- Go API `chord/nextnode/http`: Chord v2's HTTP component of Pi `6d4b7443` (`CreateHttp`, the registries `Http` and `Authenticators`, the router and its security baseline, admission with relays, signed cookies, `FileResponse`, requests and responses as descriptions, Chord's own listener, `NodeRequestOf` for apps that mount `Fetch`, the links endpoint and plugin sockets over `gorilla/websocket`). `chord/next/host` gains `RootOptions.Anonymous`, the form of Pi's explicit `principal: undefined` for a root scope with no principal. <!-- feat: chord-nextnode-http -->
