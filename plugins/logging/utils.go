// Package logging provides utility functions and interfaces for the GORM-based logging plugin
package logging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/streaming"
)

// KeyPair represents an ID-Name pair for keys
type KeyPair struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LogManager defines the main interface that combines all logging functionality
type LogManager interface {
	// GetLog retrieves a single log entry by ID (includes all fields, including raw_request/raw_response)
	GetLog(ctx context.Context, id string) (*logstore.Log, error)

	// Search searches for log entries based on filters and pagination
	Search(ctx context.Context, filters *logstore.SearchFilters, pagination *logstore.PaginationOptions) (*logstore.SearchResult, error)

	// GetSessionLogs returns paginated logs for a single parent_request_id session.
	GetSessionLogs(ctx context.Context, sessionID string, pagination *logstore.PaginationOptions) (*logstore.SessionDetailResult, error)

	// GetSessionSummary returns aggregate totals for a single parent_request_id session.
	GetSessionSummary(ctx context.Context, sessionID string) (*logstore.SessionSummaryResult, error)

	// GetStats calculates statistics for logs matching the given filters
	GetStats(ctx context.Context, filters *logstore.SearchFilters) (*logstore.SearchStats, error)

	// GetHistogram returns time-bucketed request counts for the given filters
	GetHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.HistogramResult, error)

	// GetTokenHistogram returns time-bucketed token usage for the given filters
	GetTokenHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.TokenHistogramResult, error)

	// GetCostHistogram returns time-bucketed cost data with model breakdown for the given filters
	GetCostHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.CostHistogramResult, error)

	// GetModelHistogram returns time-bucketed model usage with success/error breakdown for the given filters
	GetModelHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ModelHistogramResult, error)

	// GetLatencyHistogram returns time-bucketed latency percentiles for the given filters
	GetLatencyHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.LatencyHistogramResult, error)

	// GetTTFBHistogram returns time-bucketed streaming TTFB percentiles for the given filters
	GetTTFBHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.LatencyHistogramResult, error)

	// GetProviderCostHistogram returns time-bucketed cost data with provider breakdown for the given filters
	GetProviderCostHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderCostHistogramResult, error)

	// GetProviderTokenHistogram returns time-bucketed token usage with provider breakdown for the given filters
	GetProviderTokenHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderTokenHistogramResult, error)

	// GetProviderLatencyHistogram returns time-bucketed latency percentiles with provider breakdown for the given filters
	GetProviderLatencyHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderLatencyHistogramResult, error)

	// GetProviderTTFBHistogram returns time-bucketed streaming TTFB percentiles with provider breakdown for the given filters
	GetProviderTTFBHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderLatencyHistogramResult, error)

	// GetTTFBStats returns recent streaming TTFB stats by provider, model, and virtual key
	GetTTFBStats(ctx context.Context, filters *logstore.SearchFilters, window time.Duration, minSamples int) (*logstore.TTFBStatsResult, error)

	// GetModelRankings returns models ranked by usage with trend comparison
	GetModelRankings(ctx context.Context, filters *logstore.SearchFilters) (*logstore.ModelRankingResult, error)

	// GetDimensionRankings returns entities ranked by usage grouped by the given dimension
	GetDimensionRankings(ctx context.Context, filters *logstore.SearchFilters, dimension logstore.RankingDimension) (*logstore.DimensionRankingResult, error)

	// Get the number of dropped requests
	GetDroppedRequests(ctx context.Context) int64

	// GetAvailableModels returns all unique models from logs
	GetAvailableModels(ctx context.Context, limit int, query string) ([]string, error)

	// GetAvailableAliases returns all unique alias values from logs
	GetAvailableAliases(ctx context.Context, limit int, query string) ([]string, error)

	// GetAvailableSelectedKeys returns all unique selected key ID-Name pairs from logs
	GetAvailableSelectedKeys(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableVirtualKeys returns all unique virtual key ID-Name pairs from logs
	GetAvailableVirtualKeys(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableRoutingRules returns all unique routing rule ID-Name pairs from logs
	GetAvailableRoutingRules(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableRoutingEngines returns all unique routing engine types from logs
	GetAvailableRoutingEngines(ctx context.Context, limit int, query string) ([]string, error)

	// GetAvailableStopReasons returns all unique stop reason values from logs
	GetAvailableStopReasons(ctx context.Context, limit int, query string) ([]string, error)

	// GetAvailableTeams returns all unique team ID-Name pairs from logs
	GetAvailableTeams(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableCustomers returns all unique customer ID-Name pairs from logs
	GetAvailableCustomers(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableUsers returns all unique user IDs from logs
	GetAvailableUsers(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableBusinessUnits returns all unique business unit ID-Name pairs from logs
	GetAvailableBusinessUnits(ctx context.Context, limit int, query string) ([]KeyPair, error)

	// GetAvailableMetadataKeys returns distinct metadata keys and their values from recent logs
	GetAvailableMetadataKeys(ctx context.Context, limit int, query string) (map[string][]string, error)

	// GetDimensionCostHistogram returns time-bucketed cost data grouped by the specified dimension
	GetDimensionCostHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64, dimension logstore.HistogramDimension) (*logstore.DimensionCostHistogramResult, error)

	// GetDimensionTokenHistogram returns time-bucketed token usage grouped by the specified dimension
	GetDimensionTokenHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64, dimension logstore.HistogramDimension) (*logstore.DimensionTokenHistogramResult, error)

	// GetDimensionLatencyHistogram returns time-bucketed latency percentiles grouped by the specified dimension
	GetDimensionLatencyHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64, dimension logstore.HistogramDimension) (*logstore.DimensionLatencyHistogramResult, error)

	// DeleteLog deletes a log entry by its ID
	DeleteLog(ctx context.Context, id string) error

	// DeleteLogs deletes multiple log entries by their IDs
	DeleteLogs(ctx context.Context, ids []string) error

	// RecalculateCosts recomputes missing costs for logs matching the filters
	RecalculateCosts(ctx context.Context, filters *logstore.SearchFilters, limit int) (*RecalculateCostResult, error)
}

// PluginLogManager implements LogManager interface wrapping the plugin
type PluginLogManager struct {
	plugin *LoggerPlugin
}

func (p *PluginLogManager) GetLog(ctx context.Context, id string) (*logstore.Log, error) {
	return p.plugin.GetLog(ctx, id)
}

func (p *PluginLogManager) Search(ctx context.Context, filters *logstore.SearchFilters, pagination *logstore.PaginationOptions) (*logstore.SearchResult, error) {
	if filters == nil || pagination == nil {
		return nil, fmt.Errorf("filters and pagination cannot be nil")
	}
	return p.plugin.SearchLogs(ctx, *filters, *pagination)
}

func (p *PluginLogManager) GetSessionLogs(ctx context.Context, sessionID string, pagination *logstore.PaginationOptions) (*logstore.SessionDetailResult, error) {
	if pagination == nil {
		return nil, fmt.Errorf("pagination cannot be nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sessionID cannot be empty")
	}
	return p.plugin.GetSessionLogs(ctx, sessionID, *pagination)
}

func (p *PluginLogManager) GetSessionSummary(ctx context.Context, sessionID string) (*logstore.SessionSummaryResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sessionID cannot be empty")
	}
	return p.plugin.GetSessionSummary(ctx, sessionID)
}

func (p *PluginLogManager) GetStats(ctx context.Context, filters *logstore.SearchFilters) (*logstore.SearchStats, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetStats(ctx, *filters)
}

func (p *PluginLogManager) GetHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.HistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetTokenHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.TokenHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetTokenHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetCostHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.CostHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetCostHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetModelHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ModelHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetModelHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetLatencyHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.LatencyHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetLatencyHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetTTFBHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.LatencyHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetTTFBHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetProviderCostHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderCostHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetProviderCostHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetProviderTokenHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderTokenHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetProviderTokenHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetProviderLatencyHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderLatencyHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetProviderLatencyHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetProviderTTFBHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64) (*logstore.ProviderLatencyHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetProviderTTFBHistogram(ctx, *filters, bucketSizeSeconds)
}

func (p *PluginLogManager) GetTTFBStats(ctx context.Context, filters *logstore.SearchFilters, window time.Duration, minSamples int) (*logstore.TTFBStatsResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetTTFBStats(ctx, *filters, window, minSamples)
}

func (p *PluginLogManager) GetModelRankings(ctx context.Context, filters *logstore.SearchFilters) (*logstore.ModelRankingResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetModelRankings(ctx, *filters)
}

func (p *PluginLogManager) GetDimensionRankings(ctx context.Context, filters *logstore.SearchFilters, dimension logstore.RankingDimension) (*logstore.DimensionRankingResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetDimensionRankings(ctx, *filters, dimension)
}

func (p *PluginLogManager) GetDroppedRequests(ctx context.Context) int64 {
	return p.plugin.droppedRequests.Load()
}

// GetAvailableModels returns all unique models from logs
func (p *PluginLogManager) GetAvailableModels(ctx context.Context, limit int, query string) ([]string, error) {
	return p.plugin.GetAvailableModels(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableAliases(ctx context.Context, limit int, query string) ([]string, error) {
	return p.plugin.GetAvailableAliases(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableSelectedKeys(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableSelectedKeys(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableVirtualKeys(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableVirtualKeys(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableRoutingRules(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableRoutingRules(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableRoutingEngines(ctx context.Context, limit int, query string) ([]string, error) {
	return p.plugin.GetAvailableRoutingEngines(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableStopReasons(ctx context.Context, limit int, query string) ([]string, error) {
	return p.plugin.GetAvailableStopReasons(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableTeams(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableTeams(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableCustomers(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableCustomers(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableUsers(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableUsers(ctx, limit, query)
}

func (p *PluginLogManager) GetAvailableBusinessUnits(ctx context.Context, limit int, query string) ([]KeyPair, error) {
	return p.plugin.GetAvailableBusinessUnits(ctx, limit, query)
}

// GetDimensionCostHistogram returns time-bucketed cost data grouped by the specified dimension.
func (p *PluginLogManager) GetDimensionCostHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64, dimension logstore.HistogramDimension) (*logstore.DimensionCostHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetDimensionCostHistogram(ctx, *filters, bucketSizeSeconds, dimension)
}

// GetDimensionTokenHistogram returns time-bucketed token usage grouped by the specified dimension.
func (p *PluginLogManager) GetDimensionTokenHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64, dimension logstore.HistogramDimension) (*logstore.DimensionTokenHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetDimensionTokenHistogram(ctx, *filters, bucketSizeSeconds, dimension)
}

// GetDimensionLatencyHistogram returns time-bucketed latency percentiles grouped by the specified dimension.
func (p *PluginLogManager) GetDimensionLatencyHistogram(ctx context.Context, filters *logstore.SearchFilters, bucketSizeSeconds int64, dimension logstore.HistogramDimension) (*logstore.DimensionLatencyHistogramResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.GetDimensionLatencyHistogram(ctx, *filters, bucketSizeSeconds, dimension)
}

func (p *PluginLogManager) GetAvailableMetadataKeys(ctx context.Context, limit int, query string) (map[string][]string, error) {
	if p.plugin == nil || p.plugin.store == nil {
		return map[string][]string{}, nil
	}
	return p.plugin.store.GetDistinctMetadataKeys(ctx, limit, query)
}

// DeleteLog deletes a log from the log store
func (p *PluginLogManager) DeleteLog(ctx context.Context, id string) error {
	if p.plugin == nil || p.plugin.store == nil {
		return fmt.Errorf("log store not initialized")
	}
	return p.plugin.store.DeleteLog(ctx, id)
}

// DeleteLogs deletes multiple logs from the log store
func (p *PluginLogManager) DeleteLogs(ctx context.Context, ids []string) error {
	if p.plugin == nil || p.plugin.store == nil {
		return fmt.Errorf("log store not initialized")
	}
	return p.plugin.store.DeleteLogs(ctx, ids)
}

func (p *PluginLogManager) RecalculateCosts(ctx context.Context, filters *logstore.SearchFilters, limit int) (*RecalculateCostResult, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters cannot be nil")
	}
	return p.plugin.RecalculateCosts(ctx, *filters, limit)
}

// GetPluginLogManager returns a LogManager interface for this plugin
func (p *LoggerPlugin) GetPluginLogManager() *PluginLogManager {
	return &PluginLogManager{
		plugin: p,
	}
}

// retryOnNotFound retries a function up to 3 times with 1-second delays if it returns logstore.ErrNotFound
func retryOnNotFound(ctx context.Context, operation func() error) error {
	const maxRetries = 3
	const retryDelay = time.Second

	var lastErr error
	for attempt := range maxRetries {
		err := operation()
		if err == nil {
			return nil
		}

		// Check if the error is logstore.ErrNotFound
		if !errors.Is(err, logstore.ErrNotFound) {
			return err
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt < maxRetries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
				// Continue to next retry
			}
		}
	}

	return lastErr
}

// extractInputHistory extracts input history from request input
func (p *LoggerPlugin) extractInputHistory(request *schemas.BifrostRequest) ([]schemas.ChatMessage, []schemas.ResponsesMessage) {
	if request.ChatRequest != nil {
		return request.ChatRequest.Input, []schemas.ResponsesMessage{}
	}
	if request.ResponsesRequest != nil && len(request.ResponsesRequest.Input) > 0 {
		return []schemas.ChatMessage{}, request.ResponsesRequest.Input
	}
	if request.TextCompletionRequest != nil {
		if request.TextCompletionRequest.Input == nil {
			return []schemas.ChatMessage{}, []schemas.ResponsesMessage{}
		}
		var text string
		if request.TextCompletionRequest.Input.PromptStr != nil {
			text = *request.TextCompletionRequest.Input.PromptStr
		} else {
			var stringBuilder strings.Builder
			for _, prompt := range request.TextCompletionRequest.Input.PromptArray {
				stringBuilder.WriteString(prompt)
			}
			text = stringBuilder.String()
		}
		return []schemas.ChatMessage{
			{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{
					ContentStr: &text,
				},
			},
		}, []schemas.ResponsesMessage{}
	}
	if request.EmbeddingRequest != nil {
		// Large payload passthrough can intentionally leave Input nil to avoid
		// materializing giant request bodies. Logging should degrade gracefully.
		if request.EmbeddingRequest.Input == nil {
			return []schemas.ChatMessage{}, []schemas.ResponsesMessage{}
		}
		texts := request.EmbeddingRequest.Input.Texts

		if len(texts) == 0 && request.EmbeddingRequest.Input.Text != nil {
			texts = []string{*request.EmbeddingRequest.Input.Text}
		}

		contentBlocks := make([]schemas.ChatContentBlock, len(texts))
		for i, text := range texts {
			// Create a per-iteration copy to avoid reusing the same memory address
			t := text
			contentBlocks[i] = schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeText,
				Text: &t,
			}
		}
		return []schemas.ChatMessage{
			{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{
					ContentBlocks: contentBlocks,
				},
			},
		}, []schemas.ResponsesMessage{}
	}
	if request.RerankRequest != nil {
		query := request.RerankRequest.Query
		return []schemas.ChatMessage{
			{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{
					ContentStr: &query,
				},
			},
		}, []schemas.ResponsesMessage{}
	}
	if request.CountTokensRequest != nil && len(request.CountTokensRequest.Input) > 0 {
		return []schemas.ChatMessage{}, request.CountTokensRequest.Input
	}
	if request.CompactionRequest != nil && len(request.CompactionRequest.Input) > 0 {
		return []schemas.ChatMessage{}, request.CompactionRequest.Input
	}
	return []schemas.ChatMessage{}, []schemas.ResponsesMessage{}
}

// convertToProcessedStreamResponse converts a StreamAccumulatorResult to ProcessedStreamResponse
// for use with the logging plugin's streaming log update functionality.
func convertToProcessedStreamResponse(result *schemas.StreamAccumulatorResult, requestType schemas.RequestType) *streaming.ProcessedStreamResponse {
	if result == nil {
		return nil
	}

	// Determine stream type from request type
	var streamType streaming.StreamType
	switch requestType {
	case schemas.TextCompletionStreamRequest:
		streamType = streaming.StreamTypeText
	case schemas.ChatCompletionStreamRequest:
		streamType = streaming.StreamTypeChat
	case schemas.ResponsesStreamRequest:
		streamType = streaming.StreamTypeResponses
	case schemas.SpeechStreamRequest:
		streamType = streaming.StreamTypeAudio
	case schemas.TranscriptionStreamRequest:
		streamType = streaming.StreamTypeTranscription
	case schemas.ImageGenerationStreamRequest:
		streamType = streaming.StreamTypeImage
	case schemas.PassthroughStreamRequest:
		streamType = streaming.StreamTypePassthrough
	default:
		streamType = streaming.StreamTypeChat
	}

	// Build accumulated data
	data := &streaming.AccumulatedData{
		RequestID:             result.RequestID,
		Model:                 result.RequestedModel,
		Status:                result.Status,
		Stream:                true,
		Latency:               result.Latency,
		TimeToFirstToken:      result.TimeToFirstToken,
		OutputMessage:         result.OutputMessage,
		OutputMessages:        result.OutputMessages,
		ErrorDetails:          result.ErrorDetails,
		TokenUsage:            result.TokenUsage,
		CacheDebug:            result.CacheDebug,
		Cost:                  result.Cost,
		AudioOutput:           result.AudioOutput,
		TranscriptionOutput:   result.TranscriptionOutput,
		ImageGenerationOutput: result.ImageGenerationOutput,
		PassthroughOutput:     result.PassthroughOutput,
		FinishReason:          result.FinishReason,
		RawResponse:           result.RawResponse,
	}

	// Handle tool calls if present
	if result.OutputMessage != nil && result.OutputMessage.ChatAssistantMessage != nil {
		data.ToolCalls = result.OutputMessage.ChatAssistantMessage.ToolCalls
	}

	resp := &streaming.ProcessedStreamResponse{
		RequestID:      result.RequestID,
		StreamType:     streamType,
		Provider:       result.Provider,
		RequestedModel: result.RequestedModel,
		ResolvedModel:  result.ResolvedModel,
		Data:           data,
	}

	if result.RawRequest != nil {
		rawReq := result.RawRequest
		resp.RawRequest = &rawReq
	}

	return resp
}

// formatRoutingEngineLogs formats routing engine logs into a human-readable string.
// Format: [timestamp] [engine] - message
// Parameters:
//   - logs: Slice of routing engine log entries
//
// Returns:
//   - string: Formatted log string (empty string if no logs)
func formatRoutingEngineLogs(logs []schemas.RoutingEngineLogEntry) string {
	if len(logs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, log := range logs {
		sb.WriteString(fmt.Sprintf("[%d] [%s] - %s\n", log.Timestamp, log.Engine, log.Message))
	}
	return sb.String()
}
