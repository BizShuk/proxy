package handlers

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	metricdata "go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestTransformObserverRedactsDiagnosticDetails(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	observer, err := NewTransformObserver(logger, noop.NewMeterProvider().Meter("test"))
	require.NoError(t, err)

	observer.RecordWarning(context.Background(), "xai", model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES, model.Warning{
		Code: "downgrade", Message: "prompt secret Bearer token",
	})
	observer.RecordLoss(context.Background(), "xai", model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES, model.SemanticLoss{
		Field: "thinking.budget_tokens", Reason: "tool output secret",
	})

	output := logs.String()
	assert.Contains(t, output, "downgrade")
	assert.Contains(t, output, "thinking.budget_tokens")
	assert.NotContains(t, output, "prompt secret")
	assert.NotContains(t, output, "tool output")
	assert.NotContains(t, output, "Bearer")
}

func TestTransformObserverIncrementsCounters(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	observer, err := NewTransformObserver(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), provider.Meter("test"))
	require.NoError(t, err)

	observer.RecordWarning(context.Background(), "xai", model.FORMAT_OPENAI_CHAT, model.FORMAT_OPENAI_RESPONSES, model.Warning{Code: "warning"})
	observer.RecordLoss(context.Background(), "xai", model.FORMAT_OPENAI_CHAT, model.FORMAT_OPENAI_RESPONSES, model.SemanticLoss{Field: "messages.role"})

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &metrics))
	var names []string
	for _, scope := range metrics.ScopeMetrics {
		for _, value := range scope.Metrics {
			names = append(names, value.Name)
		}
	}
	joined := strings.Join(names, ",")
	assert.Contains(t, joined, "agentsdk.proxy.transform.warnings")
	assert.Contains(t, joined, "agentsdk.proxy.transform.losses")
}
