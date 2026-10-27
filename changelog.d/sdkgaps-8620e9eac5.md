### Fixed

- The SDK `DefaultResourceLoader` lists a tool or flag that two extensions register as an error of the extension load result (`Tool "x" conflicts with <path>`), and an extension it loads from `additionalExtensionPaths` has the source `cli`, as Pi's loader reports them. <!-- fix: 8620e9eac5 -->
