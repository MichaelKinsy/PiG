package codingagent

import "os"

// pidNamespace identifies this process's PID namespace, the link target of /proc/self/ns/pid ("pid:[4026531836]"); empty when it cannot be read.
func pidNamespace() string {
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return ""
	}
	return ns
}
