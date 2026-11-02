### Fixed

- After Ctrl+Z, `bg` and `fg`, the session returns to the terminal instead of ending with `Error: interrupted system call`: raw-mode entry and exit retry an ioctl that a stop and continue interrupted, as Node's libuv does (#397). <!-- fix: 6f165a205c -->
- A suspended session that receives SIGCONT while its process group does not own the terminal (`bg`, or `kill -CONT` from elsewhere) no longer re-enters raw mode and draws into the shell's terminal; it stops again and waits for `fg`. A terminal that went away while the session was stopped now ends it with the dead-terminal exit (129) instead of an `interrupted system call` error (#396, #397, D227). <!-- fix: b65e288aca -->
- A write to a terminal that went away (`EIO` after a hang-up, `EPIPE`, `ENOTCONN`, `ENOTTY`) now ends the session with exit 129, as Pi's stdout error listener does, instead of being discarded while the session keeps running (#397). <!-- fix: 1c1b349dbd -->
