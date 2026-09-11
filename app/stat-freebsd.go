//go:build freebsd

package app

import (
	"syscall"
	"time"
)

func statTime(stat *syscall.Stat_t) int64 {
	return time.Unix(stat.Ctimespec.Unix()).UnixMilli()
}
