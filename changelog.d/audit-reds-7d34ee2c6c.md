### Fixed

- durable harness: `ConversationTranscript.LoadEarlier` and `FillGap` waited for good once an entry loader had panicked: the load that call ran stayed registered and never ended. A load whose loader does not return is now settled, as Pi's shared promise settles when its loader throws: the calls that share it fail with `ConversationTranscript: the entry loader panicked`, the next call loads again, and the panic still reaches the caller whose call ran the loader. <!-- fix: 7d34ee2c6c1528e96cb6abc9c7156da99cd216a1 -->
