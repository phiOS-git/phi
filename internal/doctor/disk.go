package doctor

import (
	"fmt"
	"syscall"
)

// diskWarnPercent / diskFailPercent: no planning document states a
// threshold for phi doctor's disk-space check, so these are this step's own
// judgment call — flagged here, not buried in arithmetic, for cheap veto.
const (
	diskWarnPercent = 85.0
	diskFailPercent = 95.0
)

type usage struct {
	usedPercent float64
}

// diskUsageOf statfs(2)s path directly rather than shelling out to df: the
// same "read /sys and /proc, not a wrapping tool" preference S-04's
// capability probes already established, applied here to the one syscall
// this check needs. Bsize's field type differs by GOOS (int64 on Linux,
// uint32 on darwin) but converts to uint64 either way, which is what keeps
// this file build-tag-free on both the target platform and this one.
func diskUsageOf(path string) (usage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return usage{}, err
	}
	bsize := uint64(st.Bsize)
	total := st.Blocks * bsize
	free := st.Bavail * bsize
	if total == 0 {
		return usage{}, nil
	}
	used := total - free
	return usage{usedPercent: float64(used) / float64(total) * 100}, nil
}

func diskSpace() Check {
	const name = "disk space"
	u, err := diskUsageOf("/")
	if err != nil {
		return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("statfs /: %v", err)}
	}

	detail := fmt.Sprintf("/  %.1f%% used", u.usedPercent)
	status := OK
	switch {
	case u.usedPercent >= diskFailPercent:
		status = Problem
		detail += fmt.Sprintf(" (>= %.0f%%)", diskFailPercent)
	case u.usedPercent >= diskWarnPercent:
		detail += fmt.Sprintf(" (>= %.0f%% warn threshold; not yet a PROBLEM)", diskWarnPercent)
	}
	return Check{Name: name, Status: status, Detail: detail}
}
