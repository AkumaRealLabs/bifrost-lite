package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type emptyResponseTestLogger struct{}

func (emptyResponseTestLogger) Debug(string, ...any)                   {}
func (emptyResponseTestLogger) Info(string, ...any)                    {}
func (emptyResponseTestLogger) Warn(string, ...any)                    {}
func (emptyResponseTestLogger) Error(string, ...any)                   {}
func (emptyResponseTestLogger) Fatal(string, ...any)                   {}
func (emptyResponseTestLogger) SetLevel(schemas.LogLevel)              {}
func (emptyResponseTestLogger) SetOutputType(schemas.LoggerOutputType) {}
func (emptyResponseTestLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return nil
}

func emptyResponseNoopPostHookRunner(_ *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
	return result, err
}

func newTestBifrostContext(t *testing.T) *schemas.BifrostContext {
	t.Helper()
	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func newTestFastHTTPClient(serverURL string) *fasthttp.Client {
	return &fasthttp.Client{
		NoDefaultUserAgentHeader: true,
		Dial:                     fasthttp.DialDualStack,
	}
}

func requireEmptyUpstreamError(t *testing.T, err *schemas.BifrostError) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if err.IsBifrostError {
		t.Fatalf("expected upstream error, got bifrost error: %#v", err)
	}
	if err.StatusCode == nil || *err.StatusCode != 502 {
		t.Fatalf("expected status 502, got %#v", err.StatusCode)
	}
	if err.Error == nil || err.Error.Message != schemas.ErrProviderResponseEmpty {
		t.Fatalf("expected empty-response error, got %#v", err.Error)
	}
}

func collectFirstStreamOutcome(t *testing.T, stream chan *schemas.BifrostStreamChunk) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	t.Helper()
	wrapped, drainDone, err := providerUtils.CheckFirstStreamChunkForError(context.Background(), stream)
	if err != nil && drainDone != nil {
		<-drainDone
	}
	return wrapped, err
}

func TestHandleOpenAIChatCompletionRequest_EmptyResponseErrors(t *testing.T) {
	t.Run("empty content returns retriable upstream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-empty","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		resp, err := HandleOpenAIChatCompletionRequest(
			newTestBifrostContext(t),
			newTestFastHTTPClient(server.URL),
			server.URL,
			&schemas.BifrostChatRequest{Model: "gpt-4o"},
			schemas.Key{},
			nil,
			false,
			false,
			schemas.OpenAI,
			nil,
			nil,
			emptyResponseTestLogger{},
		)
		if resp != nil {
			t.Fatalf("expected nil response, got %#v", resp)
		}
		requireEmptyUpstreamError(t, err)
	})

	t.Run("tool call only remains successful", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-tool","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`)
		}))
		defer server.Close()

		resp, err := HandleOpenAIChatCompletionRequest(
			newTestBifrostContext(t),
			newTestFastHTTPClient(server.URL),
			server.URL,
			&schemas.BifrostChatRequest{Model: "gpt-4o"},
			schemas.Key{},
			nil,
			false,
			false,
			schemas.OpenAI,
			nil,
			nil,
			emptyResponseTestLogger{},
		)
		if err != nil {
			t.Fatalf("unexpected error: %#v", err)
		}
		if resp == nil || len(resp.Choices) != 1 || resp.Choices[0].Message == nil || len(resp.Choices[0].Message.ToolCalls) != 1 {
			t.Fatalf("expected tool-call response, got %#v", resp)
		}
	})
}

func TestHandleOpenAIResponsesRequest_EmptyResponseErrors(t *testing.T) {
	t.Run("empty output returns retriable upstream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"response","created_at":1,"model":"gpt-4.1","output":[]}`)
		}))
		defer server.Close()

		resp, err := HandleOpenAIResponsesRequest(
			newTestBifrostContext(t),
			newTestFastHTTPClient(server.URL),
			server.URL,
			&schemas.BifrostResponsesRequest{Model: "gpt-4.1"},
			schemas.Key{},
			nil,
			false,
			false,
			schemas.OpenAI,
			nil,
			nil,
			emptyResponseTestLogger{},
		)
		if resp != nil {
			t.Fatalf("expected nil response, got %#v", resp)
		}
		requireEmptyUpstreamError(t, err)
	})

	t.Run("empty output text returns retriable upstream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"response","created_at":1,"model":"gpt-4.1","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}]}]}`)
		}))
		defer server.Close()

		resp, err := HandleOpenAIResponsesRequest(
			newTestBifrostContext(t),
			newTestFastHTTPClient(server.URL),
			server.URL,
			&schemas.BifrostResponsesRequest{Model: "gpt-4.1"},
			schemas.Key{},
			nil,
			false,
			false,
			schemas.OpenAI,
			nil,
			nil,
			emptyResponseTestLogger{},
		)
		if resp != nil {
			t.Fatalf("expected nil response, got %#v", resp)
		}
		requireEmptyUpstreamError(t, err)
	})

	t.Run("function call output remains successful", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"response","created_at":1,"model":"gpt-4.1","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Paris\"}"}]}`)
		}))
		defer server.Close()

		resp, err := HandleOpenAIResponsesRequest(
			newTestBifrostContext(t),
			newTestFastHTTPClient(server.URL),
			server.URL,
			&schemas.BifrostResponsesRequest{Model: "gpt-4.1"},
			schemas.Key{},
			nil,
			false,
			false,
			schemas.OpenAI,
			nil,
			nil,
			emptyResponseTestLogger{},
		)
		if err != nil {
			t.Fatalf("unexpected error: %#v", err)
		}
		if resp == nil || len(resp.Output) != 1 || resp.Output[0].ResponsesToolMessage == nil || resp.Output[0].Arguments == nil {
			t.Fatalf("expected function-call response, got %#v", resp)
		}
	})
}

func TestHandleOpenAIChatCompletionStreaming_EmptyResponseErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-empty\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":0,\"total_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-empty\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream, err := HandleOpenAIChatCompletionStreaming(
		newTestBifrostContext(t),
		providerUtils.BuildStreamingClient(newTestFastHTTPClient(server.URL)),
		server.URL,
		&schemas.BifrostChatRequest{Model: "gpt-4o"},
		nil,
		nil,
		0,
		false,
		false,
		schemas.OpenAI,
		emptyResponseNoopPostHookRunner,
		nil,
		nil,
		nil,
		nil,
		nil,
		emptyResponseTestLogger{},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected handler error: %#v", err)
	}

	wrapped, firstErr := collectFirstStreamOutcome(t, stream)
	if wrapped != nil {
		t.Fatalf("expected no wrapped stream, got %#v", wrapped)
	}
	requireEmptyUpstreamError(t, firstErr)
}

func TestHandleOpenAIResponsesStreaming_EmptyResponseErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":1,\"model\":\"gpt-4.1\",\"status\":\"completed\",\"output\":[]}}\n\n")
	}))
	defer server.Close()

	stream, err := HandleOpenAIResponsesStreaming(
		newTestBifrostContext(t),
		providerUtils.BuildStreamingClient(newTestFastHTTPClient(server.URL)),
		server.URL,
		&schemas.BifrostResponsesRequest{Model: "gpt-4.1"},
		nil,
		nil,
		0,
		false,
		false,
		schemas.OpenAI,
		emptyResponseNoopPostHookRunner,
		nil,
		nil,
		nil,
		nil,
		emptyResponseTestLogger{},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected handler error: %#v", err)
	}

	wrapped, firstErr := collectFirstStreamOutcome(t, stream)
	if wrapped != nil {
		t.Fatalf("expected no wrapped stream, got %#v", wrapped)
	}
	requireEmptyUpstreamError(t, firstErr)
}
