package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bizshuk/proxy/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// TransformObserver records conversion diagnostics without logging request data.
type TransformObserver interface {
	RecordWarning(context.Context, string, model.Format, model.Format, model.Warning)
	RecordLoss(context.Context, string, model.Format, model.Format, model.SemanticLoss)
}

type transformObserver struct {
	logger   *slog.Logger
	warnings metric.Int64Counter
	losses   metric.Int64Counter
}

// NewTransformObserver creates the logger and counters shared by proxy transforms.
func NewTransformObserver(logger *slog.Logger, meter metric.Meter) (TransformObserver, error) {
	if logger == nil {
		return nil, fmt.Errorf("create transform observer: logger is required")
	}
	if meter == nil {
		return nil, fmt.Errorf("create transform observer: meter is required")
	}

	warnings, err := meter.Int64Counter("agentsdk.proxy.transform.warnings")
	if err != nil {
		return nil, fmt.Errorf("create transform warning counter: %w", err)
	}
	losses, err := meter.Int64Counter("agentsdk.proxy.transform.losses")
	if err != nil {
		return nil, fmt.Errorf("create transform loss counter: %w", err)
	}

	return &transformObserver{logger: logger, warnings: warnings, losses: losses}, nil
}

func (o *transformObserver) RecordWarning(ctx context.Context, provider string, source, target model.Format, warning model.Warning) {
	attributes := transformAttributes(provider, source, target)
	o.logger.WarnContext(ctx, "proxy transform warning",
		"provider", provider,
		"source_format", source,
		"target_format", target,
		"code", warning.Code,
	)
	o.warnings.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func (o *transformObserver) RecordLoss(ctx context.Context, provider string, source, target model.Format, loss model.SemanticLoss) {
	attributes := transformAttributes(provider, source, target)
	o.logger.WarnContext(ctx, "proxy transform semantic loss",
		"provider", provider,
		"source_format", source,
		"target_format", target,
		"field", loss.Field,
	)
	o.losses.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func transformAttributes(provider string, source, target model.Format) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("source_format", string(source)),
		attribute.String("target_format", string(target)),
	}
}
