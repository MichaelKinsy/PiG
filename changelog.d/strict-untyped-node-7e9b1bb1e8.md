### Fixed

- A tool that requires strict JSON-schema sampling and has a schema node without a type (such as `items: {}`, an unknown or any value, or a bare `const`) is now refused with a clear local error, and one that only prefers strict sampling is sent without strict mode, where before the provider rejected the request with an HTTP 400 error; a node with only an `enum` stays strict, as under Pi (upstream #12049). <!-- fix: 7e9b1bb1e8 -->
