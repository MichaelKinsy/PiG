### Fixed

- The renderer's width and height are read through one atomic snapshot. An overlay's visibility callback, an overlay control call and a mouse event on the alt screen read the two numbers from their own goroutines while a frame stored a new terminal size, so during a resize they could see the width of one size and the height of another, and the race detector reported the conformance rows of the alt screen. <!-- fix: c98be55c62 -->
