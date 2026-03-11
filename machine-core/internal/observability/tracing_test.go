package observability

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestInitPersistsSpansAndEventsToSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "machine-core-debug.sqlite")

	shutdown, err := Init(context.Background(), Config{
		Enabled:        true,
		ServiceName:    "gsender-machine-core",
		ServiceVersion: "test",
		Environment:    "test",
		DBPath:         dbPath,
		SampleRatio:    1.0,
	})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	tracer := otel.Tracer("machine-core/test")
	_, span := tracer.Start(context.Background(), "machine.test.rpc")
	span.SetAttributes(
		attribute.String("machine.session_id", "session-1"),
		attribute.String("machine.command_type", "status_report"),
	)
	span.AddEvent("machine.runtime_telemetry", trace.WithAttributes(attribute.Int("machine.metadata_keys", 3)))
	span.End()

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open debug db: %v", err)
	}
	defer db.Close()

	var spanCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM otel_spans WHERE name = ?`, "machine.test.rpc").Scan(&spanCount); err != nil {
		t.Fatalf("failed to count spans: %v", err)
	}
	if spanCount != 1 {
		t.Fatalf("expected one persisted span, got %d", spanCount)
	}

	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM otel_span_events WHERE event_name = ?`, "machine.runtime_telemetry").Scan(&eventCount); err != nil {
		t.Fatalf("failed to count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one persisted span event, got %d", eventCount)
	}
}
