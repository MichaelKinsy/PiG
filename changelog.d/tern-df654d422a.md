### Fixed

- `chord/host`: the Node facet loader no longer writes its child entry under one fixed name in the shared temporary directory. Another process that cleaned that file up, or two processes racing it, made a starting load fail with `child exited before ready`; and any other user could rewrite the file Node ran. Each load now writes the entry into a private `0700` directory of its own and removes it once the child has started.
