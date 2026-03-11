package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	machinepb "github.com/Sienci-Labs/gsender/machine-core/gen/grpc/machine/pb"
	grpcmachine "github.com/Sienci-Labs/gsender/machine-core/gen/grpc/machine/server"
	genMachine "github.com/Sienci-Labs/gsender/machine-core/gen/machine"
	"github.com/Sienci-Labs/gsender/machine-core/internal/observability"
	machineService "github.com/Sienci-Labs/gsender/machine-core/internal/service"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const defaultMachineCoreAddr = "127.0.0.1:8081"

func newMachineCoreGRPCServer() *grpc.Server {
	machineSvc := machineService.NewMemoryMachineService()
	machineEndpoints := genMachine.NewEndpoints(machineSvc)
	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	server := grpcmachine.New(machineEndpoints, nil, nil)
	machinepb.RegisterMachineServer(grpcServer, server)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)
	return grpcServer
}

func serveOnce(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind %s: %w", addr, err)
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			log.Printf("failed closing listener: %v", closeErr)
		}
	}()

	grpcServer := newMachineCoreGRPCServer()

	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	log.Printf("machine-core gRPC listening on %s", listener.Addr().String())
	if err := <-errCh; err != nil && err != grpc.ErrServerStopped {
		return err
	}
	return nil
}

func parseAddress(input string) string {
	if input == "" {
		return defaultMachineCoreAddr
	}
	if strings.HasPrefix(input, ":") {
		return "127.0.0.1" + input
	}
	if !strings.Contains(input, ":") {
		if _, err := strconv.Atoi(input); err == nil {
			return fmt.Sprintf("127.0.0.1:%s", input)
		}
	}
	return input
}

func main() {
	addr := flag.String("addr", defaultMachineCoreAddr, "Listen address for machine-core gRPC server")
	flag.Parse()

	listenAddr := parseAddress(*addr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownObservability, err := observability.Init(ctx, observability.LoadConfig())
	if err != nil {
		log.Printf("machine-core observability init failed: %v", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := shutdownObservability(shutdownCtx); shutdownErr != nil {
			log.Printf("machine-core observability shutdown failed: %v", shutdownErr)
		}
	}()

	if err := serveOnce(ctx, listenAddr); err != nil {
		log.Printf("machine-core server exit: %v", err)
		os.Exit(1)
	}
}
