### Fixed

- A `context` handler that edits a message of `event.messages` in place and then throws now leaves the edit in the request, as it does in Pi, where the handler works on a copy of the list over the one set of message objects: an edit to a message stays and a change to the list does not. Until now the failed handler's reply was dropped and the edit was lost, in every extension SDK. The loss was present in every release since v0.2.0. <!-- fix: 83928e2246 -->
