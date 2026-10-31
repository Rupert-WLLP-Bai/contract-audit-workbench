package store

import (
	"contractaudit/internal/execution"
	"contractaudit/internal/telemetry"
)

// UseTelemetry transfers lifecycle ownership to Store. Configure before serving.
func (s *Store) UseTelemetry(exporter *telemetry.Exporter) {
	if exporter != nil && s.pool != nil {
		exporter.SetRuntimeMetricsSource(s.runtimeMetrics)
	}
	previous := s.telemetry.Swap(exporter)
	if previous != exporter {
		previous.Close()
	}
}

// Telemetry returns the exporter the Store owns, so the same process can reuse
// it for the HTTP boundary instead of building a second one. It may be nil
// when telemetry is disabled.
func (s *Store) Telemetry() *telemetry.Exporter {
	return s.telemetry.Load()
}

func (s *Store) recordTelemetry(runID, attemptID string, event execution.Event) {
	s.Telemetry().Record(runID, attemptID, event)
}

func (s *Store) closeTelemetry() {
	s.UseTelemetry(nil)
}
