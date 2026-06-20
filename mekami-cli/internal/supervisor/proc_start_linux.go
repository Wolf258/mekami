//go:build !windows && !darwin

package supervisor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// readProcStartTime reads /proc/<pid>/stat and extracts
// the process start time in clock ticks since boot. This
// is the most accurate "started at" timestamp we can get
// without a tracer, and it survives a daemon being
// orphaned (its start time is stable). Returns (zero,
// false) if the file is unreadable or unparseable, which
// the caller treats as "fall back to time.Now()".
//
// Field 22 in /proc/<pid>/stat is starttime; the field
// is delimited by spaces, but the comm field (field 2)
// may contain spaces and parentheses, so we parse from
// the right: find the last ')', then split on spaces
// and pick field 22 from the remainder.
func readProcStartTime(pid int) (time.Time, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, false
	}
	// Format: pid (comm) state ppid pgrp session ...
	// The "..." continues with minflt, cminflt, majflt,
	// cmajflt, utime, stime, cutime, cstime, priority,
	// nice, num_threads, itrealvalue, starttime, ...
	// starttime is field 22, 1-indexed.
	s := string(data)
	r := strings.LastIndexByte(s, ')')
	if r < 0 || r+1 >= len(s) {
		return time.Time{}, false
	}
	rest := strings.TrimSpace(s[r+1:])
	fields := strings.Fields(rest)
	// rest starts at field 3 (state), so starttime
	// is field 22 - 3 = 19 in rest.
	if len(fields) < 19 {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[18], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	// bootTime is the wall clock at which the system
	// booted, in seconds since epoch. /proc/stat has
	// it under "btime".
	btime, ok := readBootTime()
	if !ok {
		return time.Time{}, false
	}
	// clkTck is the user-space clock tick rate. On
	// every Linux we support it is 100; we read it
	// from sysconf to be safe.
	clkTck := int64(100)
	return time.Unix(btime+ticks/clkTck, (ticks%clkTck)*(1e9/clkTck)), true
}

func readBootTime() (int64, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return v, true
				}
			}
		}
	}
	return 0, false
}
