package main

import "syscall"

// exitWithParent asks the kernel to stop the client when its launcher dies. yam stops a package
// by killing the launcher script, which would otherwise leave the client running.
func exitWithParent() {
	const prSetPdeathsig = 1
	_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prSetPdeathsig, uintptr(syscall.SIGTERM), 0)
}
