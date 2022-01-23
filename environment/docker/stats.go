package docker

import (
	"context"
	"io"
	"math"
	"time"

	"emperror.dev/errors"
	"github.com/docker/docker/api/types"
	"github.com/goccy/go-json"

	"github.com/pterodactyl/wings/environment"
)

// Uptime returns the current uptime of the container in milliseconds. If the
// container is not currently running this will return 0.
func (e *Environment) Uptime(ctx context.Context) (int64, error) {
	ins, err := e.ContainerInspect(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "environment: could not inspect container")
	}
	if !ins.State.Running {
		return 0, nil
	}
	started, err := time.Parse(time.RFC3339, ins.State.StartedAt)
	if err != nil {
		return 0, errors.Wrap(err, "environment: failed to parse container start time")
	}
	return time.Since(started).Milliseconds(), nil
}

// Attach to the instance and then automatically emit an event whenever the resource usage for the
// server process changes.
func (e *Environment) pollResources(ctx context.Context) error {
	if e.st.Load() == environment.ProcessOfflineState {
		return errors.New("cannot enable resource polling on a stopped server")
	}

	e.log().Info("starting resource polling for container")
	defer e.log().Debug("stopped resource polling for container")

	stats, err := e.client.ContainerStats(ctx, e.Id, true)
	if err != nil {
		return err
	}
	defer stats.Body.Close()

	uptime, err := e.Uptime(ctx)
	if err != nil {
		e.log().WithField("error", err).Warn("failed to calculate container uptime")
	}

	dec := json.NewDecoder(stats.Body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var v types.StatsJSON
			if err := dec.Decode(&v); err != nil {
				if err != io.EOF && !errors.Is(err, context.Canceled) {
					e.log().WithField("error", err).Warn("error while processing Docker stats output for container")
				} else {
					e.log().Debug("io.EOF encountered during stats decode, stopping polling...")
				}
				return nil
			}

			// Disable collection if the server is in an offline state and this process is still running.
			if e.st.Load() == environment.ProcessOfflineState {
				e.log().Debug("process in offline state while resource polling is still active; stopping poll")
				return nil
			}

			if !v.PreRead.IsZero() {
				uptime = uptime + v.Read.Sub(v.PreRead).Milliseconds()
			}

			st := environment.Stats{
				Uptime:      uptime,
				Memory:      calculateDockerMemory(v.MemoryStats),
				MemoryLimit: v.MemoryStats.Limit,
				CpuAbsolute: calculateDockerAbsoluteCpu(v.PreCPUStats, v.CPUStats),
				Network:     environment.NetworkStats{},
			}

			for _, nw := range v.Networks {
				st.Network.RxBytes += nw.RxBytes
				st.Network.TxBytes += nw.TxBytes
			}

			e.Events().Publish(environment.ResourceEvent, st)
		}
	}
}

// The "docker stats" CLI call does not return the same value as the types.MemoryStats.Usage
// value which can be rather confusing to people trying to compare panel usage to
// their stats output.
//
// This math is from their CLI repository in order to show the same values to avoid people
// bothering me about it. It should also reflect a slightly more correct memory value anyways.
//
// @see https://github.com/docker/cli/blob/96e1d1d6/cli/command/container/stats_helpers.go#L227-L249
func calculateDockerMemory(stats types.MemoryStats) uint64 {
	if v, ok := stats.Stats["total_inactive_file"]; ok && v < stats.Usage {
		return stats.Usage - v
	}

	if v := stats.Stats["inactive_file"]; v < stats.Usage {
		return stats.Usage - v
	}

	return stats.Usage
}

// Calculates the absolute CPU usage used by the server process on the system, not constrained
// by the defined CPU limits on the container.
//
// @see https://github.com/docker/cli/blob/aa097cf1aa19099da70930460250797c8920b709/cli/command/container/stats_helpers.go#L166
func calculateDockerAbsoluteCpu(pStats types.CPUStats, stats types.CPUStats) float64 {
	// Calculate the change in CPU usage between the current and previous reading.
	cpuDelta := float64(stats.CPUUsage.TotalUsage) - float64(pStats.CPUUsage.TotalUsage)

	// Calculate the change for the entire system's CPU usage between current and previous reading.
	systemDelta := float64(stats.SystemUsage) - float64(pStats.SystemUsage)

	// Calculate the total number of CPU cores being used.
	cpus := float64(stats.OnlineCPUs)
	if cpus == 0.0 {
		cpus = float64(len(stats.CPUUsage.PercpuUsage))
	}

	percent := 0.0
	if systemDelta > 0.0 && cpuDelta > 0.0 {
		percent = (cpuDelta / systemDelta) * cpus * 100.0
	}

	return math.Round(percent*1000) / 1000
}
