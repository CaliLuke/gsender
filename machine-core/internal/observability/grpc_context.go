package observability

import (
	"context"
	"reflect"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	goa "goa.design/goa/v3/pkg"
)

var rpcPayloadFields = []struct {
	field string
	attr  string
}{
	{field: "RPCRequestID", attr: "rpc.x-machine-core-request-id"},
	{field: "RPCAction", attr: "rpc.x-machine-core-action"},
	{field: "RPCPort", attr: "rpc.x-machine-core-port"},
	{field: "RPCSessionID", attr: "rpc.x-machine-core-session-id"},
	{field: "RPCSocketID", attr: "rpc.x-machine-core-socket-id"},
	{field: "RPCControllerEvent", attr: "rpc.x-machine-core-controller-event"},
	{field: "RPCCommandType", attr: "rpc.x-machine-core-command-type"},
	{field: "RPCOrigin", attr: "rpc.x-machine-core-origin"},
}

func EndpointTraceContextMiddleware() func(goa.Endpoint) goa.Endpoint {
	return func(next goa.Endpoint) goa.Endpoint {
		if next == nil {
			return nil
		}
		return func(ctx context.Context, req any) (any, error) {
			attrs := rpcContextAttributesFromRequest(req)
			if len(attrs) == 0 {
				return next(ctx, req)
			}
			spanName := "machine.rpc_context"
			if method, ok := ctx.Value(goa.MethodKey).(string); ok && method != "" {
				spanName = "machine." + method
			}
			ctx, span := otel.Tracer("gsender-machine-core/goa").Start(
				ctx,
				spanName,
				trace.WithAttributes(attrs...),
			)
			span.AddEvent("rpc.context", trace.WithAttributes(attrs...))
			defer span.End()
			return next(ctx, req)
		}
	}
}

func AnnotateSpanFromRequest(ctx context.Context, req any) {
	attrs := rpcContextAttributesFromRequest(req)
	if len(attrs) == 0 {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(attrs...)
	span.AddEvent("rpc.context", trace.WithAttributes(attrs...))
}

func rpcContextAttributesFromRequest(req any) []attribute.KeyValue {
	return collectRPCContextAttributes(reflect.ValueOf(req))
}

func collectRPCContextAttributes(value reflect.Value) []attribute.KeyValue {
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, len(rpcPayloadFields))
	for _, field := range rpcPayloadFields {
		member := value.FieldByName(field.field)
		if !member.IsValid() {
			continue
		}
		for member.IsValid() && member.Kind() == reflect.Pointer {
			if member.IsNil() {
				member = reflect.Value{}
				break
			}
			member = member.Elem()
		}
		if !member.IsValid() || member.Kind() != reflect.String {
			continue
		}
		trimmed := member.String()
		if trimmed == "" {
			continue
		}
		attrs = append(attrs, attribute.String(field.attr, trimmed))
	}
	if len(attrs) != 0 {
		return attrs
	}

	// Goa streaming endpoints wrap the decoded payload under Payload.
	payload := value.FieldByName("Payload")
	if payload.IsValid() {
		return collectRPCContextAttributes(payload)
	}
	return nil
}
