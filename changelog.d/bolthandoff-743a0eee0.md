### Changed

- The agent's question "does any extension handle this event?", asked for every streamed message update and every provider stream event, no longer takes the extension lock, builds a handler list and allocates: the extension keeps a lock-free set of the events it has handlers for, republished when a handler is added, removed or replaced. A 40-prompt interactive session executes about 1.8 % fewer instructions per prompt (6 paired sessions, 6 of 6 with fewer); no event, order or request changes.
