package observability

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Config struct {
	Enabled        bool
	ServiceName    string
	ServiceVersion string
	Environment    string
	DBPath         string
	SampleRatio    float64
}

func LoadConfig() Config {
	dbPath := strings.TrimSpace(os.Getenv("MACHINE_CORE_OTEL_DB_PATH"))
	if dbPath == "" {
		dbPath = filepath.Join("logs", "machine-core-debug.sqlite")
	}

	return Config{
		Enabled:        envBool("MACHINE_CORE_OTEL_ENABLED", true),
		ServiceName:    firstNonEmpty(strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")), "gsender-machine-core"),
		ServiceVersion: firstNonEmpty(strings.TrimSpace(os.Getenv("OTEL_SERVICE_VERSION")), "dev"),
		Environment:    firstNonEmpty(strings.TrimSpace(os.Getenv("ENVIRONMENT")), "local"),
		DBPath:         dbPath,
		SampleRatio:    clamp(envFloat("OTEL_TRACES_SAMPLER_ARG", 1.0), 0.0, 1.0),
	}
}

func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := NewSQLiteExporter(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	resource, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("deployment.environment", cfg.Environment),
			attribute.String("machine_core.debug_db_path", cfg.DBPath),
		),
	)
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
		sdktrace.WithBatcher(exporter),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(shutdownCtx context.Context) error {
		if err := provider.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return exporter.Shutdown(shutdownCtx)
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
