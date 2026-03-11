package main

import (
	"context"
	"database/sql"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	machinepb "github.com/Sienci-Labs/gsender/machine-core/gen/grpc/machine/pb"
	"github.com/Sienci-Labs/gsender/machine-core/internal/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	_ "modernc.org/sqlite"
)

func TestParseAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty uses default",
			input:    "",
			expected: "127.0.0.1:8081",
		},
		{
			name:     "hostless port binds loopback",
			input:    ":8081",
			expected: "127.0.0.1:8081",
		},
		{
			name:     "numeric port binds loopback",
			input:    "9090",
			expected: "127.0.0.1:9090",
		},
		{
			name:     "full address preserved",
			input:    "127.0.0.1:5001",
			expected: "127.0.0.1:5001",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseAddress(tc.input)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("parseAddress(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestMachineCoreGRPCServerLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			if !strings.Contains(closeErr.Error(), "use of closed network connection") {
				t.Fatalf("failed to close test listener: %v", closeErr)
			}
		}
	})

	grpcServer := newMachineCoreGRPCServer()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		if err := <-serverErr; err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("machine server returned unexpected error: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create machine-core client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Fatalf("failed to close grpc connection: %v", closeErr)
		}
	})

	client := machinepb.NewMachineClient(conn)
	md := metadata.New(map[string]string{
		"x-machine-core-request-id": "rpc-test-1",
		"x-machine-core-action":     "session_open",
		"x-machine-core-origin":     "jest-cncengine",
		"x-machine-core-socket-id":  "socket-telemetry",
	})
	ctx = metadata.NewOutgoingContext(ctx, md)
	devicesResp, err := client.ListDevices(ctx, &machinepb.ListDevicesRequest{})
	if err != nil {
		t.Fatalf("ListDevices RPC failed: %v", err)
	}
	if len(devicesResp.GetField()) == 0 {
		t.Fatal("expected at least one simulated device")
	}
	sessionResp, err := client.OpenSession(ctx, &machinepb.OpenSessionRequest{DeviceId: devicesResp.GetField()[0].GetId()})
	if err != nil {
		t.Fatalf("OpenSession RPC failed: %v", err)
	}
	if sessionResp.GetSession().GetId() == "" {
		t.Fatal("expected session id in OpenSession response")
	}

	closeResp, err := client.CloseSession(ctx, &machinepb.CloseSessionRequest{SessionId: sessionResp.GetSession().GetId()})
	if err != nil {
		t.Fatalf("CloseSession RPC failed: %v", err)
	}
	if !closeResp.GetOk() {
		t.Fatalf("expected close session OK=true, got %t", closeResp.GetOk())
	}
}

func TestMachineCoreGRPCServerEndToEndFlow(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	grpcServer := newMachineCoreGRPCServer()
	serverErr := make(chan error, 1)
	serverStopped := false
	go func() {
		serverErr <- grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		if serverStopped {
			return
		}
		grpcServer.Stop()
		_ = <-serverErr
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create machine-core client: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client := machinepb.NewMachineClient(conn)
	devicesResp, err := client.ListDevices(ctx, &machinepb.ListDevicesRequest{})
	if err != nil {
		t.Fatalf("ListDevices RPC failed: %v", err)
	}
	deviceID := devicesResp.GetField()[0].GetId()

	openResp, err := client.OpenSession(ctx, &machinepb.OpenSessionRequest{
		DeviceId: deviceID,
		ClientId: func() *string {
			value := "launcher"
			return &value
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession RPC failed: %v", err)
	}
	sessionID := openResp.GetSession().GetId()
	if sessionID == "" {
		t.Fatal("expected session id in OpenSession response")
	}

	resolveResp, err := client.ResolveSession(ctx, &machinepb.ResolveSessionRequest{
		DeviceId: deviceID,
		ClientId: "renderer",
	})
	if err != nil {
		t.Fatalf("ResolveSession RPC failed: %v", err)
	}
	if resolveResp.GetSession().GetId() != sessionID {
		t.Fatalf("expected resolved session id %s, got %s", sessionID, resolveResp.GetSession().GetId())
	}

	attachResp, err := client.AttachClient(ctx, &machinepb.AttachClientRequest{
		SessionId: sessionID,
		ClientId:  "renderer",
	})
	if err != nil {
		t.Fatalf("AttachClient RPC failed: %v", err)
	}
	if attachResp.GetSession().GetId() != sessionID {
		t.Fatalf("expected attached session id %s, got %s", sessionID, attachResp.GetSession().GetId())
	}

	if _, err := client.LoadFile(ctx, &machinepb.LoadFileRequest{
		SessionId: sessionID,
		Name:      "part.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile RPC failed: %v", err)
	}
	if _, err := client.StartJob(ctx, &machinepb.StartJobRequest{SessionId: sessionID}); err != nil {
		t.Fatalf("StartJob RPC failed: %v", err)
	}
	if _, err := client.PauseJob(ctx, &machinepb.PauseJobRequest{SessionId: sessionID}); err != nil {
		t.Fatalf("PauseJob RPC failed: %v", err)
	}
	if _, err := client.ResumeJob(ctx, &machinepb.ResumeJobRequest{SessionId: sessionID}); err != nil {
		t.Fatalf("ResumeJob RPC failed: %v", err)
	}
	if _, err := client.StopJob(ctx, &machinepb.StopJobRequest{SessionId: sessionID}); err != nil {
		t.Fatalf("StopJob RPC failed: %v", err)
	}
	if _, err := client.FlashFirmware(ctx, &machinepb.FlashFirmwareRequest{
		DeviceId: deviceID,
		Image:    "firmware.hex",
	}); err != nil {
		t.Fatalf("FlashFirmware RPC failed: %v", err)
	}

	stream, err := client.SubscribeEvents(ctx, &machinepb.SubscribeEventsRequest{
		SessionId: sessionID,
		ClientId:  "renderer",
	})
	if err != nil {
		t.Fatalf("SubscribeEvents RPC failed: %v", err)
	}
	var eventTypes []string
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("SubscribeEvents recv failed: %v", err)
		}
		eventTypes = append(eventTypes, event.GetType())
	}

	for _, required := range []string{
		"session_opened",
		"client_attached",
		"file_loaded",
		"job_started",
		"job_paused",
		"job_resumed",
		"job_stopped",
		"flash_started",
		"flash_completed",
	} {
		found := false
		for _, eventType := range eventTypes {
			if eventType == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected replay to include %s, got %v", required, eventTypes)
		}
	}

	closeResp, err := client.CloseSession(ctx, &machinepb.CloseSessionRequest{SessionId: sessionID})
	if err != nil {
		t.Fatalf("CloseSession RPC failed: %v", err)
	}
	if !closeResp.GetOk() {
		t.Fatalf("expected close session OK=true, got %t", closeResp.GetOk())
	}
}

func TestMachineCoreGRPCServerEmitsOTelSQLiteTraces(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "machine-core-debug.sqlite")
	shutdownObservability, err := observability.Init(context.Background(), observability.Config{
		Enabled:        true,
		ServiceName:    "gsender-machine-core",
		ServiceVersion: "test",
		Environment:    "test",
		DBPath:         dbPath,
		SampleRatio:    1.0,
	})
	if err != nil {
		t.Fatalf("observability init failed: %v", err)
	}
	defer func() {
		if shutdownErr := shutdownObservability(context.Background()); shutdownErr != nil {
			t.Fatalf("observability shutdown failed: %v", shutdownErr)
		}
	}()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	grpcServer := newMachineCoreGRPCServer()
	serverErr := make(chan error, 1)
	serverStopped := false
	go func() {
		serverErr <- grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		if serverStopped {
			return
		}
		grpcServer.Stop()
		_ = <-serverErr
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create machine-core client: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client := machinepb.NewMachineClient(conn)
	devicesResp, err := client.ListDevices(ctx, &machinepb.ListDevicesRequest{})
	if err != nil {
		t.Fatalf("ListDevices RPC failed: %v", err)
	}
	deviceID := devicesResp.GetField()[0].GetId()

	openResp, err := client.OpenSession(ctx, &machinepb.OpenSessionRequest{
		DeviceId: deviceID,
		ClientId: func() *string {
			value := "telemetry-test"
			return &value
		}(),
	})
	if err != nil {
		t.Fatalf("OpenSession RPC failed: %v", err)
	}

	if _, err := client.LoadFile(ctx, &machinepb.LoadFileRequest{
		SessionId: openResp.GetSession().GetId(),
		Name:      "trace.nc",
		Content:   "G0 X0 Y0\nG1 X1 Y1",
	}); err != nil {
		t.Fatalf("LoadFile RPC failed: %v", err)
	}

	grpcServer.Stop()
	_ = <-serverErr
	serverStopped = true
	if err := shutdownObservability(context.Background()); err != nil {
		t.Fatalf("observability shutdown failed: %v", err)
	}
	shutdownObservability = func(context.Context) error { return nil }

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open trace db: %v", err)
	}
	defer db.Close()

	var spanCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM otel_spans`).Scan(&spanCount); err != nil {
		t.Fatalf("failed to count spans: %v", err)
	}
	if spanCount == 0 {
		t.Fatal("expected persisted OTel spans for gRPC test flow")
	}

	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM otel_span_events WHERE event_name IN ('machine.session_opened', 'machine.file_loaded')`).Scan(&eventCount); err != nil {
		t.Fatalf("failed to count span events: %v", err)
	}
	if eventCount < 2 {
		t.Fatalf("expected persisted machine span events, got %d", eventCount)
	}

}
