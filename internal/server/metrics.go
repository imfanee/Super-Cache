// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Prometheus /metrics HTTP endpoint (P5.3).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Server) runPrometheusMetrics(ctx context.Context) {
	c := s.config()
	if c.MetricsPort <= 0 {
		return
	}
	bind := strings.TrimSpace(c.MetricsBind)
	if bind == "" {
		bind = "0.0.0.0"
	}
	reg := prometheus.NewRegistry()

	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "connected_clients",
			Help:      "Current number of open RESP client connections.",
		},
		func() float64 { return float64(s.stats.ConnectedClients()) },
	))
	reg.MustRegister(prometheus.NewCounterFunc(
		prometheus.CounterOpts{
			Namespace: "supercache",
			Name:      "commands_processed_total",
			Help:      "Total commands processed since process start.",
		},
		func() float64 { return float64(s.stats.TotalCommands()) },
	))
	reg.MustRegister(prometheus.NewCounterFunc(
		prometheus.CounterOpts{
			Namespace: "supercache",
			Name:      "connections_received_total",
			Help:      "Total TCP client connections accepted since process start.",
		},
		func() float64 { return float64(s.stats.TotalConnections()) },
	))
	reg.MustRegister(prometheus.NewCounterFunc(
		prometheus.CounterOpts{
			Namespace: "supercache",
			Name:      "keyspace_hits_total",
			Help:      "Total keyspace hits recorded for INFO.",
		},
		func() float64 { return float64(s.stats.KeyspaceHits()) },
	))
	reg.MustRegister(prometheus.NewCounterFunc(
		prometheus.CounterOpts{
			Namespace: "supercache",
			Name:      "keyspace_misses_total",
			Help:      "Total keyspace misses recorded for INFO.",
		},
		func() float64 { return float64(s.stats.KeyspaceMisses()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "instantaneous_ops_per_second",
			Help:      "Approximate commands per second since process start (same basis as INFO).",
		},
		func() float64 { return float64(s.stats.OpsPerSec()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "uptime_seconds",
			Help:      "Approximate process uptime in seconds.",
		},
		func() float64 { return float64(s.stats.UptimeSeconds()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "used_memory_bytes",
			Help:      "Approximate memory used by the store.",
		},
		func() float64 { return float64(s.store.MemBytes()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "db_keys",
			Help:      "Number of keys in the logical database.",
		},
		func() float64 { return float64(s.store.DBSize()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "replication_inbound_connections",
			Help:      "Inbound peer replication connections.",
		},
		func() float64 { return float64(s.stats.ConnectedPeers()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "replication_outbound_peers",
			Help:      "Number of active outbound peer mesh connections.",
		},
		func() float64 { return float64(len(s.stats.PeerAddresses())) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "bootstrap_inbound_queue_depth",
			Help:      "Depth of inbound replication queue during bootstrap (0 when idle).",
		},
		func() float64 { return float64(s.stats.BootstrapInboundQueueDepth()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "bootstrap_keys_applied",
			Help:      "Keys applied during the last bootstrap pull (0 if none).",
		},
		func() float64 { return float64(s.stats.BootstrapKeysApplied()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "command_latency_avg_us",
			Help:      "Average command latency in microseconds since process start.",
		},
		func() float64 { return float64(s.stats.LatencyAvgUs()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "command_latency_max_us",
			Help:      "Maximum command latency in microseconds since process start.",
		},
		func() float64 { return float64(s.stats.LatencyMaxUs()) },
	))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "replication_outbound_queue_depth",
			Help:      "Total depth of outbound replication queues across all peers.",
		},
		func() float64 { return float64(s.stats.ReplicationOutboundQueueDepth()) },
	))

	ver := strings.TrimSpace(s.stats.ServerVersion())
	if ver == "" {
		ver = "unknown"
	}
	buildInfo := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "supercache",
			Name:      "build_info",
			Help:      "Constant 1 with labels describing the running build.",
		},
		[]string{"version"},
	)
	reg.MustRegister(buildInfo)
	buildInfo.WithLabelValues(ver).Set(1)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	addr := net.JoinHostPort(bind, fmt.Sprintf("%d", c.MetricsPort))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Debug("metrics server shutdown", "err", err)
		}
	}()
	go func() {
		slog.Info("prometheus metrics listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server", "err", err)
		}
	}()
}
