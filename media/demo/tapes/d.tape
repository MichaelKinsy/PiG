Source tapes/settings.tape
Hide
Sleep 500ms
Type@1ms "exec ./demo-term.sh"
Enter
Sleep 1s
Type@1ms "clear"
Enter
Sleep 500ms
Show
Type "# D · Same-host Linux handoff / clean HOME / empty PATH"
Enter
Type "remote-stage.sh"
Enter
Sleep 1s
Type "cp ~/dist/pig-demo ~/clean/pig-demo"
Enter
Type "cd ~/clean"
Enter
Type "env -i HOME=$PWD PATH=/nonexistent PIG_OFFLINE=1 PI_OFFLINE=1 ./pig-demo -p hello"
Enter
Sleep 5s
Type "# No compiler, Node runtime or extension cache on PATH."
Enter
Sleep 3s
