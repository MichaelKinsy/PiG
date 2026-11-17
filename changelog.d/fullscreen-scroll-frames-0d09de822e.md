### Upgrading

- Node extensions: as in Pi (b7bb7ca905b8), a component's `render()` returns `readonly string[]`, and `Container` returns the same cached array while its children are unchanged. Extensions that modify render output in place, including `super.render()` in a `Container` subclass or an embedded component's output, must copy it first. Applies once the vendored Pi carries the change.
- Go components in the program: a `Container` or `Box` treats a child's lines as unchanged while the child returns the same slice (same first element and length) or equal rows, so a component must return a new slice when its content changes; an in-place edit of a returned slice is not seen. The slice `Container.Render` and `Box.Render` return is borrowed and read-only: copy it with `slices.Clone` before changing a line, also in a type that embeds a `Container` and edits its own `Render` result. Its capacity equals its length, so `append` is safe. A component that changes state without requesting a render stays stale while scrolling, as in Pi (it was already stale without scrolling). The extension SDKs that push lines (Python, Rust, Lisp, subprocess) pass lines by value and are unaffected.

### Changed

- Fullscreen scroll frames reuse the previous frame's component output (Pi b7bb7ca905b8, [#9549](https://github.com/earendil-works/pi/issues/9549)): the wheel, page and line keys, top and bottom, prompt jumps, scrollbar drag and selection auto-scroll request a view-only frame, `Container.Render` and `Box.Render` return the same slice while their children's lines are unchanged, and a mouse event finds its child by the cached child heights without copying. The cost of a wheel frame no longer grows with the length of the transcript.

### Fixed

- Fullscreen scrolling in long sessions no longer re-renders the whole transcript on every wheel event and key press ([#9549](https://github.com/earendil-works/pi/issues/9549)). <!-- fix: 0d09de822e -->
- Containers no longer copy every rendered line of every nesting level on every frame ([#9549](https://github.com/earendil-works/pi/issues/9549)). <!-- fix: f4700463a2 -->
- Column slicing of printable ASCII text is faster, as in Pi ([#9549](https://github.com/earendil-works/pi/issues/9549)). <!-- fix: 008b787556 -->
