### Fixed

- The thinking-level shortcut now goes to the lowest level of the model when the current level is not one of its levels (`minimal` from `off` on a model that cannot turn thinking off, as Pi), and works for every model that reasons. <!-- fix: d0de44500754 --> <!-- cause: the interactive mode re-implemented the cycle with its own index arithmetic (an unknown index clamped to 0) instead of calling the Session's function -->
