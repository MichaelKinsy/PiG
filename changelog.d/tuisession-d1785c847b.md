### Fixed

- Ctrl+P and Shift+Ctrl+P now cycle to the only model in scope when the active model is outside it, instead of saying "Only one model in scope", and cycling from outside the scope selects the first scoped model forward and the last backward instead of skipping the first (Pi #11806, D258). <!-- fix: d1785c847b -->
