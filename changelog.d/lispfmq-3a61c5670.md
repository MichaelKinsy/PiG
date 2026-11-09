### Fixed

- The file mutation conformance rows no longer take a slow registration in the host's queue for a file an SDK task holds. They ran a host mutation while a task that had not entered yet was waiting to, and reported "a host mutation ran 89.667ms after the SDK task started" on Windows for the isolated Lisp SDK. The rows now register in the queue and ask whether their place was admitted, which does not depend on how long the registration took. <!-- fix: 3a61c5670 -->
