package query

import "syscall"

// detachedSysProcAttr configures a child process to survive its parent's
// exit as an orphan (Setsid: starts the child in its own new session, so
// it is not a member of the parent's process group and receives no signal
// the parent's own exit or the shell's SIGHUP might otherwise propagate).
// currency.go's spawnCurrencyRefresh is the first caller; kept as its own
// small file since a second caller (any other provider needing the same
// "start real work, do not wait for it, outlive this process" shape) has
// something to import directly instead of copying it.
//
// Unix-only (Setsid is a real field of syscall.SysProcAttr on Linux and
// Darwin both — phiOS itself is Arch Linux only, phi/CLAUDE.md; Darwin
// support here is incidental, only because this repository is developed
// from a Mac, not because phi ships there).
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
