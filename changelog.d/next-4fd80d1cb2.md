### Fixed

- On Windows, PiG finds PowerShell in its installation directories when it is not on `PATH`: PowerShell 7 under `%ProgramFiles%\PowerShell\7` after `PATH`, and Windows PowerShell under `%SystemRoot%\System32\WindowsPowerShell\v1.0` as the last fallback, on any drive (Pi 61060ddf, #9490). <!-- fix: 4fd80d1cb2 -->
- On Windows, the legacy WSL `bash.exe` launcher is recognized under the actual Windows directory in `SystemRoot` (`System32` and `Sysnative`), not only under `C:\Windows`, so its command goes over stdin (Pi 61060ddf, #9490). <!-- fix: 4fd80d1cb2 -->
- On Windows, an empty `SystemRoot` takes `C:\Windows` for `taskkill.exe`, as an unset one already did, in the shell tools and the Durable node environment (Pi 61060ddf, #9490). <!-- fix: 4fd80d1cb2 -->
- On Windows, shells that are runnable Microsoft Store aliases are accepted: a configured shell path, `bash.exe` and `pwsh.exe` found by `where`, and the Git Bash locations are checked with an attribute check (`access(F_OK)`) instead of a stat that follows the alias (Pi 28bea006, nodejs/node#36790). Not yet run on a Windows host. <!-- fix: 4fd80d1cb2 -->
