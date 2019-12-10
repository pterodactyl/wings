package main

import (
	"github.com/docker/docker/pkg/parsers/kernel"
	"runtime"
)

type SystemInformation struct {
	Version       string `json:"version"`
	KernelVersion string `json:"kernel_version"`
	Architecture  string `json:"architecture"`
	OS            string `json:"os"`
	CpuCount      int    `json:"cpu_count"`
}

func GetSystemInformation() (*SystemInformation, error) {
	k, err := kernel.GetKernelVersion()
	if err != nil {
		return nil, err
	}

	s := &SystemInformation{
		Version:       Version,
		KernelVersion: k.String(),
		Architecture:  runtime.GOARCH,
		OS:            runtime.GOOS,
		CpuCount:      runtime.NumCPU(),
	}

	return s, nil
}
