### Fixed

- After an answer, interactive mode no longer draws three more frames that repaint nothing and only move and hide the cursor (two direct renders at the end of the turn and one requested by `agent_settled`). Pi ends its output with the frame that carries the end of the reply; PiG now does too, which also stops a terminal multiplexer or a script that waits for output to go quiet from waiting a frame longer after every answer. <!-- fix: 18e3b43f1bc06e704351f3d1c193b5367a90ff72 -->
