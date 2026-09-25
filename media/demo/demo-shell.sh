# Loaded only by demo-term.sh after environment isolation.
source "$DEMO_ROOT/env.sh"
export PS1='\[\e[38;5;114m\]demo ❯\[\e[0m\] '
export HISTFILE=/dev/null
unset PROMPT_COMMAND
cd "$HOME/work"
