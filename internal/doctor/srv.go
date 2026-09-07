package doctor

import (
	"fmt"
	"os"
	"syscall"
)

// srvMount is added to Report only on mini (Run), where /srv lives on a
// LUKS volume unlocked by hand after every boot (master plan §9.2's note:
// "dopo ogni riavvio i servizi che dipendono da /srv non partono finché non
// sblocchi"). Until that happens /srv is an ordinary, empty directory on
// the root filesystem, not a missing feature — but this still reports
// Problem, because a timer running doctor unattended wants to know /srv is
// still not there, whatever the reason, rather than have that go quiet.
func srvMount() Check {
	const name = "/srv mount"

	rootStat, err := os.Stat("/")
	if err != nil {
		return Check{Name: name, Status: Unknown, Detail: err.Error()}
	}
	srvStat, err := os.Stat("/srv")
	if err != nil {
		return Check{Name: name, Status: Problem, Detail: fmt.Sprintf("/srv: %v", err)}
	}

	rootSys, ok1 := rootStat.Sys().(*syscall.Stat_t)
	srvSys, ok2 := srvStat.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return Check{Name: name, Status: Unknown, Detail: "cannot determine device ids"}
	}
	if rootSys.Dev == srvSys.Dev {
		return Check{Name: name, Status: Problem, Detail: "/srv is not a separate mount — the encrypted volume is likely still locked (unlock by hand after every reboot, master plan §9.2)"}
	}

	u, err := diskUsageOf("/srv")
	if err != nil {
		return Check{Name: name, Status: OK, Detail: "mounted"}
	}
	return Check{Name: name, Status: OK, Detail: fmt.Sprintf("mounted, %.1f%% used", u.usedPercent)}
}
