# Python factory example

This extension exposes one conventional `new_extension()` factory in
`python_factory.py`. It registers the `hello` tool.

Validate it:

```bash
pig install --validate-only --json examples/extensions/python-factory
```

Pig imports the factory into a generated Python runner. A Python factory can
share a Python runtime cell. The source does not contain a standalone event
loop.
