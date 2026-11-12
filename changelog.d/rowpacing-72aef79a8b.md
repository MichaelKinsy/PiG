### Changed

- A spinner that animates while nothing else is drawn (the model has not answered yet, a tool is running) steps on time again, every 80 ms as in Pi. The wait of up to 40 ms that lets a streaming reply carry the spinner steps in its own frames (D619) now applies only while frames are being drawn; before, it also delayed every step of an idle spinner, and the longest time without output while waiting for a reply was a step and the wait instead of one step.
