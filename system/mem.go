package system

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// MemoryUsagePercent returns the percentage of system memory currently used,
// along with total and available bytes. It parses /proc/meminfo on Linux.
// If MemAvailable is not present (older kernels), it falls back to
// MemFree + Buffers + Cached.
func MemoryUsagePercent() (usedPercent float64, totalBytes uint64, availBytes uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()

	var memTotal, memAvailable, memFree, buffers, cached uint64

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		key := strings.TrimSuffix(fields[0], ":")
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		// Values in /proc/meminfo are in kB; convert to bytes.
		val *= 1024

		switch key {
		case "MemTotal":
			memTotal = val
		case "MemAvailable":
			memAvailable = val
		case "MemFree":
			memFree = val
		case "Buffers":
			buffers = val
		case "Cached":
			cached = val
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, err
	}

	if memTotal == 0 {
		return 0, 0, 0, nil
	}

	if memAvailable == 0 {
		memAvailable = memFree + buffers + cached
	}

	usedPercent = float64(memTotal-memAvailable) / float64(memTotal) * 100
	return usedPercent, memTotal, memAvailable, nil
}
