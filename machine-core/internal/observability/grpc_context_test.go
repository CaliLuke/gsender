package observability

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	machinepb "github.com/Sienci-Labs/gsender/machine-core/gen/grpc/machine/pb"
	grpcserver "github.com/Sienci-Labs/gsender/machine-core/gen/grpc/machine/server"
	gen "github.com/Sienci-Labs/gsender/machine-core/gen/machine"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/metadata"
)

func stringPtr(value string) *string {
	return &value
}

func TestAnnotateSpanFromRequestPersistsRPCContextEvent(t *testing.T) {
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

	payload := &gen.OpenSessionPayload{
		DeviceID:     "sim://loopback",
		RPCRequestID: stringPtr("rpc-test-1"),
		RPCAction:    stringPtr("session_open"),
		RPCOrigin:    stringPtr("jest-cncengine"),
		RPCSocketID:  stringPtr("socket-telemetry"),
	}

	ctx, span := otel.Tracer("machine-core/test").Start(context.Background(), "machine.test.rpc_context")
	AnnotateSpanFromRequest(ctx, payload)
	span.End()

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open debug db: %v", err)
	}
	defer db.Close()

	var attributesJSON string
	if err := db.QueryRow(`SELECT attributes_json FROM otel_span_events WHERE event_name = 'rpc.context' LIMIT 1`).Scan(&attributesJSON); err != nil {
		t.Fatalf("failed to fetch rpc context event: %v", err)
	}
	if !strings.Contains(attributesJSON, `"rpc.x-machine-core-origin":"jest-cncengine"`) {
		t.Fatalf("expected persisted rpc origin context, got %s", attributesJSON)
	}
}

func TestDecodeOpenSessionRequestMapsMetadataIntoPayload(t *testing.T) {
	message := &machinepb.OpenSessionRequest{DeviceId: "sim://loopback"}
	md := metadata.New(map[string]string{
		"x-machine-core-request-id": "rpc-test-1",
		"x-machine-core-action":     "session_open",
		"x-machine-core-origin":     "jest-cncengine",
		"x-machine-core-socket-id":  "socket-telemetry",
	})

	decoded, err := grpcserver.DecodeOpenSessionRequest(context.Background(), message, md)
	if err != nil {
		t.Fatalf("DecodeOpenSessionRequest returned error: %v", err)
	}
	payload, ok := decoded.(*gen.OpenSessionPayload)
	if !ok {
		t.Fatalf("expected *gen.OpenSessionPayload, got %T", decoded)
	}
	if payload.RPCRequestID == nil || *payload.RPCRequestID != "rpc-test-1" {
		t.Fatalf("unexpected request id: %+v", payload.RPCRequestID)
	}
	if payload.RPCAction == nil || *payload.RPCAction != "session_open" {
		t.Fatalf("unexpected action: %+v", payload.RPCAction)
	}
	if payload.RPCOrigin == nil || *payload.RPCOrigin != "jest-cncengine" {
		t.Fatalf("unexpected origin: %+v", payload.RPCOrigin)
	}
	if payload.RPCSocketID == nil || *payload.RPCSocketID != "socket-telemetry" {
		t.Fatalf("unexpected socket id: %+v", payload.RPCSocketID)
	}
}
