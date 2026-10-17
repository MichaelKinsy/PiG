# Node factory example

`index.mjs` is a Pi-compatible extension: a default-exported function that receives the `pi` extension API and registers the `hello` tool and `count_words`, a tool with a required `text` parameter that throws when it is missing. Node extensions import Pi's API from `@earendil-works/pi-coding-agent`, which PiG's Node runtime provides, so this file needs no `npm install`.

Validate it:

```bash
pig install --validate-only --json examples/extensions/node-factory
```

Every example in `examples/extensions` is also loaded into a real host and its tool called in `test/extension-conformance` (`TestExamplesRunThroughTheHost`).
