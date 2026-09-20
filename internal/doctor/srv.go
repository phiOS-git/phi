package doctor

import (
	"fmt"
	"os"
	"syscall"
)

// srvMount is added to Report only on mini, where /srv lives on a LUKS
// volume unlocked by hand after each boot. Until unlocked, /srv is an
// ordinary empty directory on the root filesystem. This reports Problem
// because an unattended timer wants to know /srv is still not mounted.
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
		return Check{Name: name, Status: Problem, Detail: "/srv is not a separate mount — the encrypted volume is likely still locked"}
	}

	u, err := diskUsageOf("/srv")
	if err != nil {
		return Check{Name: name, Status: OK, Detail: "mounted"}
	}
	return Check{Name: name, Status: OK, Detail: fmt.Sprintf("mounted, %.1f%% used", u.usedPercent)}
}
