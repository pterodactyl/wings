package system

import (
	"runtime"

	"github.com/docker/docker/pkg/parsers/kernel"
)

type Information struct {
	Version       string `json:"version"`
	KernelVersion string `json:"kernel_version"`
	Architecture  string `json:"architecture"`
	OS            string `json:"os"`
	CpuCount      int    `json:"cpu_count"`
}

func GetSystemInformation() (*Information, error) {
	k, err := kernel.GetKernelVersion()
	if err != nil {
		return nil, err
	}

	s := &Information{
		Version:       Version,
		KernelVersion: k.String(),
		Architecture:  runtime.GOARCH,
		OS:            runtime.GOOS,
		CpuCount:      runtime.NumCPU(),
	}

	return s, nil
}
