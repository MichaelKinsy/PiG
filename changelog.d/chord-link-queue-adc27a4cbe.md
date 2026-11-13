### Fixed

- A Chord host's link no longer drops the messages its transport delivered before closing when the close arrives while the link's listener is still being handed the messages queued for it: a host that set its listener with messages already waiting (a worker's IPC link queues until its start message) lost the rest of that queue and was told of the close first. The listener now gets them in order and the close after them, as in Pi, where the hand-over is finished before a close is heard; a link closed from its own side still drops what it has not delivered. <!-- fix: adc27a4cbe -->
