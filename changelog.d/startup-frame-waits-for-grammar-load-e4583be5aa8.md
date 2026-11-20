### Fixed

- A start whose first frame was slow, as on a loaded machine, no longer writes a third frame, a bare cursor placement, after the frame that shows the startup notice (for example "No models available"). Pi runs that frame after the syntax grammars have loaded and asks for one frame for both; PiG now keeps a due frame until the grammar load's repaint request has arrived and draws the same single frame. Nothing changes on the screen; only the bytes written to the terminal do. <!-- fix: e4583be5aa8 -->
