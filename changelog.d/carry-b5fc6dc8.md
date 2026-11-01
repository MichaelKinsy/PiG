### Fixed

- Tool output and `!` command output no longer show the parameters of control sequences with a `<`, `=` or `>` prefix as text, for example `35;59;1M` from a mouse report `ESC[<35;59;1M` or `0;276;0c` from a terminal reply `ESC[>0;276;0c`. Text after the keypad-mode sequences `ESC=` and `ESC>` is still kept. Carried forward from Pi b5fc6dc8 (earendil-works/pi#11377), past the 1.4.0+b3a29f11 pin. <!-- fix: b5fc6dc8 -->
