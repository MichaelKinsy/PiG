### Fixed

- Fixed a chord host root failing with `superseded: code version N is not retained` although it was started in the current code version: the host picked the current code version and counted the root in it in two separate steps, and a reload's retirement of that code version could fall between them. The root now starts in one step, as upstream's does. <!-- fix: 735c36cdbf -->
