### Fixed
- Ctrl+V pastes clipboard text in Termux. PiG read `termux-clipboard-get` only where the platform is `linux`, and Termux reports `android`. A failed copy in Termux now also shows the Termux:API install hint on Android.
- `!` command and RPC `bash` output no longer keeps fragments of color codes, such as a stray `m`, when an escape sequence is split across output chunks. PiG holds a trailing unfinished sequence of up to 256 characters until the next chunk or the end of the command.
