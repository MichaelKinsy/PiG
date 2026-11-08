### Upgrading

- Go extension SDK: `MessageRenderOptions` and `EntryRenderOptions` are no longer comparable, because each gains a `State` map, the renderer state Pi keeps per message or entry in a WeakMap. Compare the fields you need (`Expanded`, `OutputPad`), or use `reflect.DeepEqual`; do not use either type as a map key or compare with `==`. `pig extension upgrade` names the change.
