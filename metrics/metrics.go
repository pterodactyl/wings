package metrics

import (
	"github.com/apex/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/pterodactyl/wings/config"
	"net/http"
	"time"
)

type Metrics struct {
	handler http.Handler
}

const (
	namespace = "pterodactyl"
	subsystem = "wings"
)

var (
	bootTimeSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "boot_time_seconds",
		Help:      "Boot time of this instance since epoch (1970)",
	})
	timeSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "time_seconds",
		Help:      "System time in seconds since epoch (1970)",
	})

	ServerStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "server_status",
	}, []string{"server_id"})
	ServerCPU = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "server_cpu",
	}, []string{"server_id"})
	ServerMemory = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "server_memory",
	}, []string{"server_id"})
	ServerNetworkRx = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "server_network_rx",
	}, []string{"server_id"})
	ServerNetworkTx = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "server_network_tx",
	}, []string{"server_id"})

	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "http_requests_total",
	}, []string{"method", "route_path", "raw_path", "raw_query", "code"})
)

func Initialize(done chan bool) {
	bootTimeSeconds.Set(float64(time.Now().UnixNano()) / 1e9)
	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				// Received a "signal" on the done channel.
				log.Debug("metrics: done")
				return
			case t := <-ticker.C:
				// Update the current time.
				timeSeconds.Set(float64(t.UnixNano()) / 1e9)
			}
		}
	}()
	if err := http.ListenAndServe(config.Get().Metrics.Bind, promhttp.Handler()); err != nil && err != http.ErrServerClosed {
		log.WithField("error", err).Error("failed to start metrics server")
	}
}

// DeleteServer will remove any existing labels from being scraped by Prometheus.
// Any previously scraped data will still be persisted by Prometheus.
func DeleteServer(sID string) {
	ServerStatus.DeleteLabelValues(sID)
	ServerCPU.DeleteLabelValues(sID)
	ServerMemory.DeleteLabelValues(sID)
	ServerNetworkRx.DeleteLabelValues(sID)
	ServerNetworkTx.DeleteLabelValues(sID)
}

// ResetServer will reset a server's metrics to their default values except the status.
func ResetServer(sID string) {
	ServerCPU.WithLabelValues(sID).Set(0)
	ServerMemory.WithLabelValues(sID).Set(0)
	ServerNetworkRx.WithLabelValues(sID).Set(0)
	ServerNetworkTx.WithLabelValues(sID).Set(0)
}
