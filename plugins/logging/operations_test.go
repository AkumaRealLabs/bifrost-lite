package logging

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
	"github.com/maximhq/bifrost/framework/streaming"
)

type testLogger struct{}

func (testLogger) Debug(string, ...any)                   {}
func (testLogger) Info(string, ...any)                    {}
func (testLogger) Warn(string, ...any)                    {}
func (testLogger) Error(string, ...any)                   {}
func (testLogger) Fatal(string, ...any)                   {}
func (testLogger) SetLevel(schemas.LogLevel)              {}
func (testLogger) SetOutputType(schemas.LoggerOutputType) {}
func (testLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func newTestStore(t *testing.T) logstore.LogStore {
	t.Helper()

	store, err := logstore.NewLogStore(context.Background(), &logstore.Config{
		Enabled: true,
		Type:    logstore.LogStoreTypeSQLite,
		Config: &logstore.SQLiteConfig{
			Path: filepath.Join(t.TempDir(), "logging.db"),
		},
	}, testLogger{})
	if err != nil {
		t.Fatalf("NewLogStore() error = %v", err)
	}
	return store
}

func TestPostLLMHookNoPendingErrorPreservesMetadata(t *testing.T) {
	store := newTestStore(t)
	loggingHeaders := []string{"x-custom-log"}
	plugin, err := Init(context.Background(), &Config{LoggingHeaders: &loggingHeaders}, testLogger{}, store, nil)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-error-no-pending")
	ctx.SetValue(schemas.BifrostContextKeyRequestHeaders, map[string]string{
		"x-bf-lh-tenant": "acme",
		"x-custom-log":   "custom-value",
	})
	ctx.SetValue(schemas.BifrostContextKeyDimensions, map[string]string{
		"region": "us-east",
	})

	statusCode := 500
	bifrostErr := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error:          &schemas.ErrorField{Message: "provider failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RequestType:            schemas.ChatCompletionRequest,
			Provider:               schemas.OpenAI,
			OriginalModelRequested: "gpt-4o",
			ResolvedModelUsed:      "gpt-4o",
		},
	}

	_, _, err = plugin.PostLLMHook(ctx, nil, bifrostErr)
	if err != nil {
		t.Fatalf("PostLLMHook() error = %v", err)
	}
	if err := plugin.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), "req-error-no-pending")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.Status != "error" {
		t.Fatalf("expected error status, got %q", logEntry.Status)
	}
	if logEntry.MetadataParsed == nil {
		t.Fatalf("expected metadata to be persisted")
	}
	if got := logEntry.MetadataParsed["tenant"]; got != "acme" {
		t.Fatalf("expected tenant metadata acme, got %#v", got)
	}
	if got := logEntry.MetadataParsed["x-custom-log"]; got != "custom-value" {
		t.Fatalf("expected configured header metadata custom-value, got %#v", got)
	}
	if got := logEntry.MetadataParsed["region"]; got != "us-east" {
		t.Fatalf("expected dimension metadata us-east, got %#v", got)
	}
}

func TestPostLLMHookStreamingErrorPreservesHeaderMetadata(t *testing.T) {
	store := newTestStore(t)
	loggingHeaders := []string{"x-custom-log"}
	plugin, err := Init(context.Background(), &Config{LoggingHeaders: &loggingHeaders}, testLogger{}, store, nil)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-stream-error-metadata")
	ctx.SetValue(schemas.BifrostContextKeyRequestHeaders, map[string]string{
		"x-custom-log":   "custom-value",
		"x-bf-lh-user":   `{"device_id":"device-1","session_id":"session-1"}`,
		"x-bf-lh-tag":    "from-header",
		"x-bf-lh-shared": "from-header",
	})
	ctx.SetValue(schemas.BifrostContextKeyDimensions, map[string]string{
		"environment": "staging",
	})

	req := &schemas.BifrostRequest{
		RequestType: schemas.ResponsesStreamRequest,
		ResponsesRequest: &schemas.BifrostResponsesRequest{
			Provider: schemas.Bedrock,
			Model:    "us.anthropic.claude-opus-4-7",
			Params:   &schemas.ResponsesParameters{},
		},
	}
	if _, _, err = plugin.PreLLMHook(ctx, req); err != nil {
		t.Fatalf("PreLLMHook() error = %v", err)
	}

	statusCode := 500
	bifrostErr := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error:          &schemas.ErrorField{Message: "stream failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RequestType:            schemas.ResponsesStreamRequest,
			Provider:               schemas.Bedrock,
			OriginalModelRequested: "us.anthropic.claude-opus-4-7",
			ResolvedModelUsed:      "us.anthropic.claude-opus-4-7",
		},
	}
	if _, _, err = plugin.PostLLMHook(ctx, nil, bifrostErr); err != nil {
		t.Fatalf("PostLLMHook() error = %v", err)
	}
	if err := plugin.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), "req-stream-error-metadata")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.Status != "error" {
		t.Fatalf("expected error status, got %q", logEntry.Status)
	}
	if logEntry.MetadataParsed == nil {
		t.Fatalf("expected metadata to be persisted")
	}
	if got := logEntry.MetadataParsed["user"]; got != `{"device_id":"device-1","session_id":"session-1"}` {
		t.Fatalf("expected user metadata from header, got %#v", got)
	}
	if got := logEntry.MetadataParsed["tag"]; got != "from-header" {
		t.Fatalf("expected tag metadata from header, got %#v", got)
	}
	if got := logEntry.MetadataParsed["x-custom-log"]; got != "custom-value" {
		t.Fatalf("expected configured header metadata custom-value, got %#v", got)
	}
	if got := logEntry.MetadataParsed["shared"]; got != "from-header" {
		t.Fatalf("expected shared metadata from header, got %#v", got)
	}
	if got := logEntry.MetadataParsed["environment"]; got != "staging" {
		t.Fatalf("expected dimension metadata staging, got %#v", got)
	}
}

// TestPostLLMHookCancelledStreamLogsCost verifies #3357 at the logging layer: a
// streaming request cancelled mid-flight (result==nil) whose error carries the
// partial usage the provider already processed (BifrostError.ExtraFields.BilledUsage)
// must produce a log row with status="error", the consumed tokens, AND an
// accurate cost computed from the datasheet rates.
func TestPostLLMHookCancelledStreamLogsCost(t *testing.T) {
	store := newTestStore(t)

	// Pricing manager loaded from the committed datasheet testdata via an
	// offline file:// URL (no network).
	abs, err := filepath.Abs("../../framework/modelcatalog/datasheet/testdata/pricing.json")
	if err != nil {
		t.Fatalf("resolve testdata path: %v", err)
	}
	ds := datasheet.New(nil, testLogger{}, datasheet.Config{URL: "file://" + abs})
	if err := ds.LoadFromURLIntoMemory(context.Background()); err != nil {
		t.Fatalf("load pricing datasheet: %v", err)
	}
	pricingManager := modelcatalog.NewTestCatalogWithDatasheet(ds)

	plugin, err := Init(context.Background(), &Config{}, testLogger{}, store, pricingManager)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-cancel-cost")

	req := &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionStreamRequest,
		ChatRequest: &schemas.BifrostChatRequest{
			Provider: schemas.OpenAI,
			Model:    "gpt-4o",
			Params:   &schemas.ChatParameters{},
		},
	}
	if _, _, err = plugin.PreLLMHook(ctx, req); err != nil {
		t.Fatalf("PreLLMHook() error = %v", err)
	}

	const promptTokens, completionTokens = 100, 50
	statusCode := 499 // client closed request (mid-stream cancel)
	bifrostErr := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error:          &schemas.ErrorField{Message: "client disconnected"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RequestType:            schemas.ChatCompletionStreamRequest,
			Provider:               schemas.OpenAI,
			OriginalModelRequested: "gpt-4o",
			ResolvedModelUsed:      "gpt-4o",
			// Provider processed these tokens before the client disconnected.
			BilledUsage: &schemas.BifrostLLMUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		},
	}
	if _, _, err = plugin.PostLLMHook(ctx, nil, bifrostErr); err != nil {
		t.Fatalf("PostLLMHook() error = %v", err)
	}
	if err := plugin.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	entry, err := store.FindByID(context.Background(), "req-cancel-cost")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if entry.Status != "error" {
		t.Fatalf("expected error status, got %q", entry.Status)
	}
	if entry.TokenUsageParsed == nil {
		t.Fatalf("expected token usage recorded from BilledUsage on the cancel path")
	}
	if entry.TotalTokens != promptTokens+completionTokens {
		t.Fatalf("expected total_tokens %d, got %d", promptTokens+completionTokens, entry.TotalTokens)
	}
	if entry.Cost == nil {
		t.Fatalf("expected a cost to be logged for a cancelled request that consumed tokens (#3357)")
	}
	// gpt-4o testdata rates: input 2.5e-6/token, output 1e-5/token.
	want := float64(promptTokens)*2.5e-6 + float64(completionTokens)*1e-5
	if diff := *entry.Cost - want; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("logged cost %v does not match datasheet-computed cost %v", *entry.Cost, want)
	}
}

// newTestPricingManager builds a ModelCatalog backed by the committed pricing
// testdata via an offline file:// URL (no network).
func newTestPricingManager(t *testing.T) *modelcatalog.ModelCatalog {
	t.Helper()
	abs, err := filepath.Abs("../../framework/modelcatalog/datasheet/testdata/pricing.json")
	if err != nil {
		t.Fatalf("resolve testdata path: %v", err)
	}
	ds := datasheet.New(nil, testLogger{}, datasheet.Config{URL: "file://" + abs})
	if err := ds.LoadFromURLIntoMemory(context.Background()); err != nil {
		t.Fatalf("load pricing datasheet: %v", err)
	}
	return modelcatalog.NewTestCatalogWithDatasheet(ds)
}

// TestApplyErrorBillingFromBilledUsage_ComputesCostWhenTokensAlreadyParsed guards
// the case where stream accumulation already captured token usage on a failed
// request but no cost was computed: cost must still be backfilled, and the
// already-parsed token counters must be left untouched (not double-applied).
func TestApplyErrorBillingFromBilledUsage_ComputesCostWhenTokensAlreadyParsed(t *testing.T) {
	store := newTestStore(t)
	plugin, err := Init(context.Background(), &Config{}, testLogger{}, store, newTestPricingManager(t))
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	const promptTokens, completionTokens = 100, 50
	entry := &logstore.Log{
		Provider:         string(schemas.OpenAI),
		Model:            "gpt-4o",
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		TokenUsageParsed: &schemas.BifrostLLMUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}
	billed := entry.TokenUsageParsed

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	plugin.applyErrorBillingFromBilledUsage(ctx, entry, billed, schemas.ChatCompletionStreamRequest)

	if entry.Cost == nil {
		t.Fatal("expected cost to be computed even though token usage was already parsed")
	}
	// gpt-4o testdata rates: input 2.5e-6/token, output 1e-5/token.
	want := float64(promptTokens)*2.5e-6 + float64(completionTokens)*1e-5
	if diff := *entry.Cost - want; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("cost %v does not match datasheet-computed %v", *entry.Cost, want)
	}
	if entry.PromptTokens != promptTokens || entry.TotalTokens != promptTokens+completionTokens {
		t.Fatalf("token counters mutated: prompt=%d total=%d", entry.PromptTokens, entry.TotalTokens)
	}
}

// TestApplyErrorBillingFromBilledUsage_FillsTokensAndCostWhenUnparsed pins the
// original behaviour: when no usage was captured yet, both tokens and cost are
// backfilled from BilledUsage.
func TestApplyErrorBillingFromBilledUsage_FillsTokensAndCostWhenUnparsed(t *testing.T) {
	store := newTestStore(t)
	plugin, err := Init(context.Background(), &Config{}, testLogger{}, store, newTestPricingManager(t))
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	const promptTokens, completionTokens = 100, 50
	entry := &logstore.Log{Provider: string(schemas.OpenAI), Model: "gpt-4o"}
	billed := &schemas.BifrostLLMUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	plugin.applyErrorBillingFromBilledUsage(ctx, entry, billed, schemas.ChatCompletionStreamRequest)

	if entry.TokenUsageParsed == nil || entry.TotalTokens != promptTokens+completionTokens {
		t.Fatalf("expected tokens backfilled, got %+v", entry.TokenUsageParsed)
	}
	if entry.Cost == nil {
		t.Fatal("expected cost computed on the unparsed path")
	}
}

func TestBuildInitialLogEntryPreservesMetadata(t *testing.T) {
	metadata := map[string]any{"tenant": "acme"}
	entry := buildInitialLogEntry(&PendingLogData{
		RequestID:     "req-initial-metadata",
		Timestamp:     time.Now().UTC(),
		FallbackIndex: 1,
		InitialData: &InitialLogData{
			Provider: string(schemas.OpenAI),
			Model:    "gpt-4o",
			Object:   string(schemas.ChatCompletionRequest),
			Metadata: metadata,
		},
	})

	if entry.MetadataParsed == nil {
		t.Fatalf("expected metadata on initial log entry")
	}
	if got := entry.MetadataParsed["tenant"]; got != "acme" {
		t.Fatalf("expected tenant metadata acme, got %#v", got)
	}
}

// TestActiveStreamSurvivesCleanup is the regression test for the prod issue where
// streaming requests running longer than the pending TTL had their in-memory
// pending entry evicted mid-flight (causing the final log row to be lost and a
// per-chunk "no pending log data found" warning). An entry whose CreatedAt is
// older than the TTL but whose LastActivity is recent must NOT be reaped.
func TestActiveStreamSurvivesCleanup(t *testing.T) {
	store := newTestStore(t)
	plugin, err := Init(context.Background(), &Config{}, testLogger{}, store, nil)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := plugin.Cleanup(); cleanupErr != nil {
			t.Errorf("Cleanup() error = %v", cleanupErr)
		}
	})

	oldCreatedAt := time.Now().Add(-pendingLogTTL - time.Minute)
	pending := &PendingLogData{
		RequestID:   "req-active-stream",
		Timestamp:   oldCreatedAt,
		Status:      "processing",
		InitialData: &InitialLogData{Object: "chat.completion.chunk"},
		CreatedAt:   oldCreatedAt,
	}
	// Simulate a chunk that arrived just now: request started long ago but is
	// still actively streaming.
	pending.LastActivity.Store(time.Now().UnixNano())
	plugin.pendingLogsEntries.Store("req-active-stream", pending)

	plugin.cleanupStalePendingLogs()

	if _, ok := plugin.pendingLogsEntries.Load("req-active-stream"); !ok {
		t.Fatal("expected actively-streaming pending entry to survive cleanup")
	}
}

// TestIdlePendingEntryEvicted verifies the reaper still removes genuinely dead
// requests: an entry whose CreatedAt AND LastActivity are both older than the
// TTL (no chunk activity for the whole idle window) must be deleted.
func TestIdlePendingEntryEvicted(t *testing.T) {
	store := newTestStore(t)
	plugin, err := Init(context.Background(), &Config{}, testLogger{}, store, nil)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := plugin.Cleanup(); cleanupErr != nil {
			t.Errorf("Cleanup() error = %v", cleanupErr)
		}
	})

	stale := time.Now().Add(-pendingLogTTL - time.Minute)
	pending := &PendingLogData{
		RequestID:   "req-idle",
		Timestamp:   stale,
		Status:      "processing",
		InitialData: &InitialLogData{Object: "chat.completion.chunk"},
		CreatedAt:   stale,
	}
	pending.LastActivity.Store(stale.UnixNano())
	plugin.pendingLogsEntries.Store("req-idle", pending)

	plugin.cleanupStalePendingLogs()

	if _, ok := plugin.pendingLogsEntries.Load("req-idle"); ok {
		t.Fatal("expected idle pending entry to be evicted by cleanup")
	}
}

func TestUpdateLogEntryPreservesResponsesInputContentSummary(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
	}

	requestID := "req-1"
	now := time.Now().UTC()
	inputText := "request-side text"
	initial := &InitialLogData{
		Object:   "responses",
		Provider: "openai",
		Model:    "gpt-4o-mini",
		ResponsesInputHistory: []schemas.ResponsesMessage{{
			Content: &schemas.ResponsesMessageContent{
				ContentStr: &inputText,
			},
		}},
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	responsesText := "responses output"
	update := &UpdateLogData{
		Status: "success",
		ResponsesOutput: []schemas.ResponsesMessage{{
			Content: &schemas.ResponsesMessageContent{
				ContentStr: &responsesText,
			},
		}},
	}

	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, "", update, true); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if !strings.Contains(logEntry.ContentSummary, inputText) {
		t.Fatalf("expected content summary to preserve responses input, got %q", logEntry.ContentSummary)
	}
	if strings.Contains(logEntry.ContentSummary, responsesText) {
		t.Fatalf("expected content summary to avoid overwriting with responses output-only data, got %q", logEntry.ContentSummary)
	}
}

func TestUpdateLogEntryUpdatesContentSummaryForChatOutput(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
	}

	requestID := "req-chat"
	now := time.Now().UTC()
	initial := &InitialLogData{
		Object:   "chat_completion",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	chatText := "assistant output"
	update := &UpdateLogData{
		Status: "success",
		ChatOutput: &schemas.ChatMessage{
			Role: schemas.ChatMessageRoleAssistant,
			Content: &schemas.ChatMessageContent{
				ContentStr: &chatText,
			},
		},
	}

	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, "", update, true); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if !strings.Contains(logEntry.ContentSummary, chatText) {
		t.Fatalf("expected content summary to include chat output, got %q", logEntry.ContentSummary)
	}
}

func TestUpdateLogEntrySuppressesChatOutputWhenContentLoggingDisabled(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
	}

	requestID := "req-chat-disabled"
	now := time.Now().UTC()
	initial := &InitialLogData{
		Object:   "chat_completion",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	chatText := "assistant output should not be logged"
	update := &UpdateLogData{
		Status: "success",
		ChatOutput: &schemas.ChatMessage{
			Role: schemas.ChatMessageRoleAssistant,
			Content: &schemas.ChatMessageContent{
				ContentStr: &chatText,
			},
		},
	}

	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, "", update, false); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.OutputMessage != "" {
		t.Fatalf("expected output_message to be suppressed, got %q", logEntry.OutputMessage)
	}
	if strings.Contains(logEntry.ContentSummary, chatText) {
		t.Fatalf("expected content summary to suppress chat output, got %q", logEntry.ContentSummary)
	}
}

func TestStoreOrEnqueueRetryPreservesAllEntries(t *testing.T) {
	// Simulate fallback/retry scenario where multiple PostLLMHook calls
	// store entries under the same traceID. All entries must be preserved.
	plugin := &LoggerPlugin{
		logger:     testLogger{},
		writeQueue: make(chan *writeQueueEntry, 10),
	}

	traceID := "trace-retry-test"
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyTraceID, traceID)

	// Simulate 3 retry attempts storing entries under the same traceID
	entry1 := &logstore.Log{ID: "req-attempt-1", Model: "gpt-4o"}
	entry2 := &logstore.Log{ID: "req-attempt-2", Model: "gpt-4o"}
	entry3 := &logstore.Log{ID: "req-attempt-3", Model: "claude-3-5-sonnet"}

	plugin.storeOrEnqueueEntry(ctx, entry1, nil)
	plugin.storeOrEnqueueEntry(ctx, entry2, nil)
	plugin.storeOrEnqueueEntry(ctx, entry3, nil)

	// Verify all 3 entries are stored
	val, ok := plugin.pendingLogsToInject.Load(traceID)
	if !ok {
		t.Fatal("expected pending entries for traceID, got none")
	}
	pending, ok := val.(*pendingInjectEntries)
	if !ok {
		t.Fatal("expected *pendingInjectEntries type")
	}
	if len(pending.entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(pending.entries))
	}
	if pending.entries[0].ID != "req-attempt-1" || pending.entries[1].ID != "req-attempt-2" || pending.entries[2].ID != "req-attempt-3" {
		t.Fatalf("entries not in expected order: %v, %v, %v", pending.entries[0].ID, pending.entries[1].ID, pending.entries[2].ID)
	}

	// Now test Inject flushes all entries with plugin logs attached
	trace := &schemas.Trace{
		TraceID: traceID,
		PluginLogs: []schemas.PluginLogEntry{
			{PluginName: "hello-world", Level: schemas.LogLevelInfo, Message: "test log"},
		},
	}

	if err := plugin.Inject(context.Background(), trace); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	// Verify all 3 entries were enqueued to writeQueue
	if len(plugin.writeQueue) != 3 {
		t.Fatalf("expected 3 entries in writeQueue, got %d", len(plugin.writeQueue))
	}

	// Verify plugin logs were attached to each entry
	for i := 0; i < 3; i++ {
		qe := <-plugin.writeQueue
		if qe.log.PluginLogs == "" {
			t.Fatalf("entry %d: expected PluginLogs to be set", i)
		}
	}

	// Verify pendingLogsToInject was cleaned up
	if _, ok := plugin.pendingLogsToInject.Load(traceID); ok {
		t.Fatal("expected pendingLogsToInject to be cleaned up after Inject")
	}
}

// TestContentLoggingEnabledHelper verifies precedence: ctx override > global config > default-enabled.
func TestContentLoggingEnabledHelper(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name          string
		globalDisable *bool
		ctxOverride   *bool // nil = don't set the key
		want          bool
	}{
		{"no config no override → enabled", nil, nil, true},
		{"global disable=false no override → enabled", boolPtr(false), nil, true},
		{"global disable=true no override → disabled", boolPtr(true), nil, false},
		{"ctx override=false global disable=true → enabled", boolPtr(true), boolPtr(false), true},
		{"ctx override=true global disable=false → disabled", boolPtr(false), boolPtr(true), false},
		{"ctx override=true nil global → disabled", nil, boolPtr(true), false},
		{"ctx override=false nil global → enabled", nil, boolPtr(false), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &LoggerPlugin{disableContentLogging: tc.globalDisable}

			var ctx *schemas.BifrostContext
			if tc.ctxOverride != nil {
				ctx = schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
				ctx.SetValue(schemas.BifrostContextKeyAllowPerRequestStorageOverride, true)
				ctx.SetValue(schemas.BifrostContextKeyDisableContentLogging, *tc.ctxOverride)
			}

			got := p.contentLoggingEnabled(ctx)
			if got != tc.want {
				t.Errorf("contentLoggingEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestContentLoggingEnabledHelperNilCtx verifies nil context falls back to global config.
func TestContentLoggingEnabledHelperNilCtx(t *testing.T) {
	disabled := true
	p := &LoggerPlugin{disableContentLogging: &disabled}
	if p.contentLoggingEnabled(nil) {
		t.Error("expected false with nil ctx and global disable=true")
	}
}

// TestUpdateLogEntryPerRequestOverrideEnablesContent verifies that passing contentLoggingEnabled=true
// to updateLogEntry stores output even when the plugin's global toggle is disabled.
func TestUpdateLogEntryPerRequestOverrideEnablesContent(t *testing.T) {
	store := newTestStore(t)
	disabled := true
	plugin := &LoggerPlugin{
		store:                 store,
		logger:                testLogger{},
		disableContentLogging: &disabled, // global: off
	}

	requestID := "req-per-request-enable"
	now := time.Now().UTC()
	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, &InitialLogData{
		Object:   "chat_completion",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	chatText := "should be stored via per-request override"
	update := &UpdateLogData{
		Status: "success",
		ChatOutput: &schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleAssistant,
			Content: &schemas.ChatMessageContent{ContentStr: &chatText},
		},
	}

	// Explicitly pass true — simulates the per-request ctx override enabling content logging
	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, "", update, true); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.OutputMessage == "" {
		t.Error("expected output_message to be stored when contentLoggingEnabled=true override is used")
	}
}

// TestUpdateLogEntryPerRequestOverrideDisablesContent verifies that passing contentLoggingEnabled=false
// suppresses output even when the plugin's global toggle is enabled.
func TestUpdateLogEntryPerRequestOverrideDisablesContent(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
		// global: nil → content logging on by default
	}

	requestID := "req-per-request-disable"
	now := time.Now().UTC()
	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, &InitialLogData{
		Object:   "chat_completion",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	chatText := "should NOT be stored"
	update := &UpdateLogData{
		Status: "success",
		ChatOutput: &schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleAssistant,
			Content: &schemas.ChatMessageContent{ContentStr: &chatText},
		},
	}

	// Explicitly pass false — simulates x-bf-disable-content-logging: true on this request
	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, "", update, false); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.OutputMessage != "" {
		t.Errorf("expected output_message to be suppressed, got %q", logEntry.OutputMessage)
	}
}

// TestApplyNonStreamingOutputToEntryContentLoggingDisabled verifies that output fields are
// suppressed when contentLoggingEnabled=false.
func TestApplyNonStreamingOutputToEntryContentLoggingDisabled(t *testing.T) {
	plugin := &LoggerPlugin{}
	entry := &logstore.Log{}

	chatText := "should not appear"
	result := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			Choices: []schemas.BifrostResponseChoice{
				{
					ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
						Message: &schemas.ChatMessage{
							Role:    schemas.ChatMessageRoleAssistant,
							Content: &schemas.ChatMessageContent{ContentStr: &chatText},
						},
					},
				},
			},
			ExtraFields: schemas.BifrostResponseExtraFields{
				RequestType: schemas.ChatCompletionRequest,
			},
		},
	}

	plugin.applyNonStreamingOutputToEntry(entry, result, false, false)

	if entry.OutputMessageParsed != nil {
		t.Error("expected OutputMessageParsed to be nil when contentLoggingEnabled=false")
	}
	if entry.TTFBMs != nil {
		t.Fatalf("expected non-streaming response to leave TTFBMs nil, got %v", *entry.TTFBMs)
	}
}

func TestApplyStreamingOutputToEntryStoresTTFBAndTTFT(t *testing.T) {
	plugin := &LoggerPlugin{}
	entry := &logstore.Log{}

	plugin.applyStreamingOutputToEntry(entry, &streaming.ProcessedStreamResponse{
		Data: &streaming.AccumulatedData{
			Latency:          4200,
			TimeToFirstByte:  321,
			TimeToFirstToken: 1234,
		},
	}, false, true)

	if entry.TTFBMs == nil {
		t.Fatal("expected streaming response to set TTFBMs")
	}
	if *entry.TTFBMs != 321 {
		t.Fatalf("TTFBMs = %v, want 321", *entry.TTFBMs)
	}
	if entry.TTFTMs == nil {
		t.Fatal("expected streaming response to set TTFTMs")
	}
	if *entry.TTFTMs != 1234 {
		t.Fatalf("TTFTMs = %v, want 1234", *entry.TTFTMs)
	}
}

// TestApplyNonStreamingOutputToEntryContentLoggingEnabled verifies that output fields are
// stored when contentLoggingEnabled=true regardless of the global plugin config.
func TestApplyNonStreamingOutputToEntryContentLoggingEnabled(t *testing.T) {
	disabled := true
	plugin := &LoggerPlugin{disableContentLogging: &disabled} // global off, but explicit true passed
	entry := &logstore.Log{}

	chatText := "should appear"
	result := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			Choices: []schemas.BifrostResponseChoice{
				{
					ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
						Message: &schemas.ChatMessage{
							Role:    schemas.ChatMessageRoleAssistant,
							Content: &schemas.ChatMessageContent{ContentStr: &chatText},
						},
					},
				},
			},
			ExtraFields: schemas.BifrostResponseExtraFields{
				RequestType: schemas.ChatCompletionRequest,
			},
		},
	}

	plugin.applyNonStreamingOutputToEntry(entry, result, false, true)

	if entry.OutputMessageParsed == nil {
		t.Error("expected OutputMessageParsed to be set when contentLoggingEnabled=true")
	}
}
