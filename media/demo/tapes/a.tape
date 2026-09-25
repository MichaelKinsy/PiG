Source tapes/settings.tape
Hide
Sleep 500ms
Type@1ms "exec ./demo-term.sh"
Enter
Sleep 1s
Type@1ms "cp ~/original/stats.py stats.py; clear"
Enter
Sleep 500ms
Show
Type "# A · Pi / chain · LOCAL FAUX PROVIDER"
Enter
Type "pi --version"
Enter
Sleep 1s
Type "pi -e ~/chain"
Enter
Sleep 2s
Type "/chain fix the failing tests"
Enter
# VHS Buffer() reads scrollback row zero, not the visible viewport. Session/tool assertions run after recording.
Sleep 9s
Sleep 2s
Ctrl+D
Sleep 1s
Type "python3 -m unittest -v test_stats"
Enter
Sleep 1s
Sleep 3s
