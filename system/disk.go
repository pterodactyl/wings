package system

import (
	"golang.org/x/sys/unix"
)

// DiskUsagePercent returns the percentage of disk space used, plus total and
// available bytes for the filesystem containing the given path. The path itself
// does not need to exist as long as its parent directory does.
func DiskUsagePercent(path string) (usedPercent float64, totalBytes uint64, availBytes uint64, err error) {
	var stat unix.Statfs_t
	if err = unix.Statfs(path, &stat); err != nil {
		return 0, 0, 0, err
	}

	totalBytes = stat.Blocks * uint64(stat.Bsize)
	availBytes = stat.Bavail * uint64(stat.Bsize)
	if totalBytes == 0 {
		return 0, 0, 0, nil
	}

	usedPercent = float64(totalBytes-availBytes) / float64(totalBytes) * 100
	return usedPercent, totalBytes, availBytes, nil
}
