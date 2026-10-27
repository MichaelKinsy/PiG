#!/usr/bin/env bash
# Temporary: make test-sdk-lisp with the log kept in .sdk.log
make test-sdk-lisp > .sdk.log 2>&1
echo "exit: $?; $(wc -l < .sdk.log) lines"
tail -40 .sdk.log
