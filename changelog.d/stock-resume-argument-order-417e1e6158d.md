### Fixed

- A reopened session drew a tool call's generic card (a tool with no renderer of its own, or a card that fell back) with the call's arguments in alphabetical order; Pi and a live PiG turn draw them in the order the model sent them. The transcript encoded the arguments from a Go map, which sorts its keys, instead of the stored member order. <!-- fix: 417e1e6158d -->
