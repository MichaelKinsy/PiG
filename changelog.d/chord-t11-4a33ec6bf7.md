### Fixed

- The Chord app kit gives its plugins component the web surface's assets path (`/_chord/web/assets/`), as Pi's app does: a web plugin's `import icon from "./icon.svg?url"` evaluated to a root-relative file name that no route served, and now to the URL the surface serves it at. <!-- fix: 4a33ec6bf7 -->
- The Chord terminal launcher holds the sandbox's frames from the moment a request that asks the user arrives, not once the goroutine answering it runs: a frame the sandbox wrote right after its request could be drawn over the launcher's prompt. A second prompt request while one is open is refused at once (`terminal: a prompt is open`), and the launcher sets its resize and signal restores on its loop, where a finishing launcher reads them. <!-- fix: 4a33ec6bf7 -->
