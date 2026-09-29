# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
# Run through render.sh. The shell has an isolated HOME and no credentials.
# Fullscreen keeps VHS Screen waits in the visible xterm buffer (VHS ignores scrollback offsets).
# The faux provider returns fixed answers; the Bash tool and extension are real.

Output quickstart-raw.mp4
Output quickstart.ascii
Require bash
Require go

Set Shell "bash"
Set FontFamily "DejaVu Sans Mono"
Set FontSize 20
Set LineHeight 1.1
Set Width 1280
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 30ms
Set Framerate 15
Set CursorBlink false
Set WaitTimeout 120s
Set WaitPattern /\$\s*$/

Hide
Type `export PS1='$ ' HISTFILE=/dev/null; cd "$QS_SOURCE"; clear; printf '\n  PiG quickstart\n\n  Local rehearsal / unpublished release\n  Source build + credential-free faux provider\n  Real terminal, Bash tool, sessions and extension\n\n  Public install paths: see QUICKSTART.md\n'`
Enter
Wait
Show
Sleep 4s
Ctrl+L

Type "# 1. Build from a source checkout"
Enter
Type `go build -o "$HOME/.local/bin/pig" ./cmd/pig`
Enter
Wait
Type "pig --version"
Enter
Wait
Sleep 2s
Ctrl+L

Type "# 2. Select the local test model (not a live AI service)"
Enter
Type "export PIG_TEST_FAUX=1 PIG_OFFLINE=1"
Enter
Type "cd ~/demo && pig --tui-mode fullscreen --model test-faux/faux-1"
Enter
# The current Pi-compatible header replaces the former Ready. banner.
Wait+Screen /Press ctrl\+o to show full startup help and loaded resources\./
Sleep 2s
Type "What is 20+22?"
Enter
Wait+Screen /\n\s*42\s*\n/
Sleep 3s
Type "Run: expr 20 + 22"
Enter
Wait+Screen /Took [0-9.]+s/
Sleep 4s

Type "/name Quickstart"
Enter
Sleep 1s
Type "/new"
Enter
Wait+Screen /New session started/
Type "/resume"
Enter
Wait+Screen /Resume Session \(Current Folder\)/
Sleep 4s
Enter
Wait+Screen /Resumed session from/
Wait+Screen /\$ expr 20 \+ 22\s*\n\s*42/
Sleep 2s
Type "reply with exactly: Resumed."
Enter
Wait+Screen /\n\s*Resumed\.\s*\n/
Sleep 2s
Ctrl+D
Wait
Ctrl+L

Type "# 3. Print mode: one answer, then exit"
Enter
Type `pig --model test-faux/faux-1 -p "What is 20+22?"`
Enter
Wait
Sleep 2s
Ctrl+L

Type "# 4. Scaffold and explicitly load a Go extension"
Enter
Type "pig extension init ./hello --lang go"
Enter
Wait
Sleep 2s
Type "pig -e ./hello --tui-mode fullscreen --model test-faux/faux-1"
Enter
Wait+Screen /hello loaded/
Sleep 2s
Type "/hello"
Enter
Wait+Screen /hello from hello/
Sleep 3s
Ctrl+D
Wait
Ctrl+L

Type "# 5. Source builds are updated by rebuilding, not self-update"
Enter
Type `pig update; echo "exit=$?"`
Enter
Wait
Sleep 4s
Type "# QUICKSTART.md has verified commands and install limitations."
Enter
Sleep 3s
