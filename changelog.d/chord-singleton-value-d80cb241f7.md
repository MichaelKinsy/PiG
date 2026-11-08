### Fixed

<<<<<<<< HEAD:changelog.d/chord-singleton-value-d80cb241f77.md
- A Chord singleton state document's handle reads its value in a section of the host's loop, as its watch does: Go code that reads it from a job's goroutine no longer races the object's own commits and its seal when the host closes. <!-- fix: d80cb241f77 -->
========
- A Chord singleton state document's handle reads its value in a section of the host's loop, as its watch does: Go code that reads it from a job's goroutine no longer races the object's own commits and its seal when the host closes. <!-- fix: d80cb241f7 -->
>>>>>>>> origin/follow/pi-main:changelog.d/chord-singleton-value-d80cb241f7.md
