package system

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/acobaugh/osrelease"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/parsers/kernel"
)

type Information struct {
	Version string            `json:"version"`
	Docker  DockerInformation `json:"docker"`
	System  System            `json:"system"`
}

type DockerInformation struct {
	Version    string           `json:"version"`
	Cgroups    DockerCgroups    `json:"cgroups"`
	Containers DockerContainers `json:"containers"`
	Storage    DockerStorage    `json:"storage"`
	Runc       DockerRunc       `json:"runc"`
}

type DockerCgroups struct {
	Driver  string `json:"driver"`
	Version string `json:"version"`
}

type DockerContainers struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Stopped int `json:"stopped"`
}

type DockerStorage struct {
	Driver     string `json:"driver"`
	Filesystem string `json:"filesystem"`
}

type DockerRunc struct {
	Version string `json:"version"`
}

type System struct {
	Architecture    string `json:"architecture"`
	CPUThreads      int    `json:"cpu_threads"`
	MemoryBytes     int64  `json:"memory_bytes"`
	MemoryUsedBytes int64  `json:"memory_used_bytes"`
	KernelVersion   string `json:"kernel_version"`
	OS              string `json:"os"`
	OSType          string `json:"os_type"`
}

func GetSystemInformation() (*Information, error) {
	k, err := kernel.GetKernelVersion()
	if err != nil {
		return nil, err
	}

	version, info, err := GetDockerInfo(context.Background())
	if err != nil {
		return nil, err
	}

	release, err := osrelease.Read()
	if err != nil {
		return nil, err
	}

	var os string
	if release["PRETTY_NAME"] != "" {
		os = release["PRETTY_NAME"]
	} else if release["NAME"] != "" {
		os = release["NAME"]
	} else {
		os = info.OperatingSystem
	}

	var filesystem string
	for _, v := range info.DriverStatus {
		if v[0] != "Backing Filesystem" {
			continue
		}
		filesystem = v[1]
		break
	}

	totalMemoryBytes, usedMemoryBytes := getSystemMemoryUsage(info.MemTotal)

	return &Information{
		Version: Version,
		Docker: DockerInformation{
			Version: version.Version,
			Cgroups: DockerCgroups{
				Driver:  info.CgroupDriver,
				Version: info.CgroupVersion,
			},
			Containers: DockerContainers{
				Total:   info.Containers,
				Running: info.ContainersRunning,
				Paused:  info.ContainersPaused,
				Stopped: info.ContainersStopped,
			},
			Storage: DockerStorage{
				Driver:     info.Driver,
				Filesystem: filesystem,
			},
			Runc: DockerRunc{
				Version: info.RuncCommit.ID,
			},
		},
		System: System{
			Architecture:    runtime.GOARCH,
			CPUThreads:      runtime.NumCPU(),
			MemoryBytes:     totalMemoryBytes,
			MemoryUsedBytes: usedMemoryBytes,
			KernelVersion:   k.String(),
			OS:              os,
			OSType:          runtime.GOOS,
		},
	}, nil
}

func getSystemMemoryUsage(fallbackTotal int64) (int64, int64) {
	total := fallbackTotal
	used := int64(0)

	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return total, used
	}

	var memTotal int64
	var memAvailable int64

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			memTotal = parseMeminfoKiBLine(line)
			continue
		}

		if strings.HasPrefix(line, "MemAvailable:") {
			memAvailable = parseMeminfoKiBLine(line)
		}
	}

	if memTotal > 0 {
		total = memTotal
	}

	if memTotal > 0 && memAvailable > 0 && memAvailable <= memTotal {
		used = memTotal - memAvailable
	}

	return total, used
}

func parseMeminfoKiBLine(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}

	kib, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || kib < 0 {
		return 0
	}

	return kib * 1024
}

func GetDockerInfo(ctx context.Context) (types.Version, system.Info, error) {
	// TODO: find a way to re-use the client from the docker environment.
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return types.Version{}, system.Info{}, err
	}
	defer c.Close()

	dockerVersion, err := c.ServerVersion(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}

	dockerInfo, err := c.Info(ctx)
	if err != nil {
		return types.Version{}, system.Info{}, err
	}

	return dockerVersion, dockerInfo, nil
}
