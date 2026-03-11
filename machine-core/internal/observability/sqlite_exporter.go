package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	_ "modernc.org/sqlite"
)

const createTraceTablesSQL = `
CREATE TABLE IF NOT EXISTS otel_spans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    parent_span_id TEXT,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    start_time TEXT NOT NULL,
    end_time TEXT NOT NULL,
    duration_ms REAL NOT NULL,
    status_code TEXT,
    status_message TEXT,
    attributes_json TEXT,
    resource_json TEXT,
    scope_name TEXT,
    scope_version TEXT
);
CREATE INDEX IF NOT EXISTS idx_otel_spans_trace_id ON otel_spans(trace_id);
CREATE INDEX IF NOT EXISTS idx_otel_spans_name ON otel_spans(name);
CREATE INDEX IF NOT EXISTS idx_otel_spans_start_time ON otel_spans(start_time);

CREATE TABLE IF NOT EXISTS otel_span_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    event_name TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    attributes_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_otel_span_events_trace_id ON otel_span_events(trace_id);
CREATE INDEX IF NOT EXISTS idx_otel_span_events_span_id ON otel_span_events(span_id);
CREATE INDEX IF NOT EXISTS idx_otel_span_events_name ON otel_span_events(event_name);
`

type SQLiteExporter struct {
	mu     sync.Mutex
	db     *sql.DB
	dbPath string
}

func NewSQLiteExporter(dbPath string) (*SQLiteExporter, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("sqlite exporter mkdir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=DELETE&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("sqlite exporter open: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(createTraceTablesSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite exporter schema: %w", err)
	}

	return &SQLiteExporter{
		db:     db,
		dbPath: dbPath,
	}, nil
}

func (e *SQLiteExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx, err := e.db.Begin()
	if err != nil {
		return err
	}

	spanStmt, err := tx.Prepare(`
INSERT INTO otel_spans (
	trace_id, span_id, parent_span_id, name, kind,
	start_time, end_time, duration_ms, status_code, status_message,
	attributes_json, resource_json, scope_name, scope_version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer spanStmt.Close()

	eventStmt, err := tx.Prepare(`
INSERT INTO otel_span_events (
	trace_id, span_id, event_name, timestamp, attributes_json
) VALUES (?, ?, ?, ?, ?)
`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer eventStmt.Close()

	for _, span := range spans {
		traceID := span.SpanContext().TraceID().String()
		spanID := span.SpanContext().SpanID().String()
		parentSpanID := span.Parent().SpanID().String()
		if !span.Parent().IsValid() {
			parentSpanID = ""
		}

		if _, err := spanStmt.Exec(
			traceID,
			spanID,
			parentSpanID,
			span.Name(),
			span.SpanKind().String(),
			span.StartTime().UTC().Format(time.RFC3339Nano),
			span.EndTime().UTC().Format(time.RFC3339Nano),
			float64(span.EndTime().Sub(span.StartTime()).Microseconds())/1000.0,
			span.Status().Code.String(),
			span.Status().Description,
			marshalAttributes(span.Attributes()),
			marshalResource(span),
			span.InstrumentationScope().Name,
			span.InstrumentationScope().Version,
		); err != nil {
			_ = tx.Rollback()
			return err
		}

		for _, event := range span.Events() {
			if _, err := eventStmt.Exec(
				traceID,
				spanID,
				event.Name,
				event.Time.UTC().Format(time.RFC3339Nano),
				marshalAttributes(event.Attributes),
			); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}

	return tx.Commit()
}

func (e *SQLiteExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return nil
	}
	err := e.db.Close()
	e.db = nil
	return err
}

func marshalAttributes(attrs []attribute.KeyValue) string {
	if len(attrs) == 0 {
		return ""
	}
	values := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value.AsInterface()
	}
	buf, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(buf)
}

func marshalResource(span sdktrace.ReadOnlySpan) string {
	if span.Resource() == nil {
		return ""
	}
	return marshalAttributes(span.Resource().Attributes())
}
