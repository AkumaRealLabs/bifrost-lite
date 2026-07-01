package utils

import (
	"context"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

// CheckFirstStreamChunkForError reads initial non-output chunks from a streaming channel
// to detect errors returned inside HTTP 200 SSE streams (e.g., providers that send rate
// limit errors as SSE events instead of HTTP 429).
//
// If an error arrives before visible output, it drains the source channel in the
// background (so the provider goroutine can exit cleanly) and returns the error for
// synchronous handling, enabling retries and fallbacks. The returned drainDone channel
// is closed once the drain completes — callers must wait on it before releasing any
// resources (e.g., plugin pipelines) that the provider goroutine's postHookRunner may
// still reference.
//
// If visible output starts first, it returns a wrapped channel that re-emits buffered
// initial chunks followed by all remaining chunks from the source. drainDone is closed
// when the wrapper goroutine finishes forwarding the source stream.
//
// If the source channel is closed immediately (empty stream), it returns a
// nil channel with nil error. drainDone is already closed.
//
// The ctx argument cancels the background forwarding goroutine if the consumer
// abandons the returned wrapped channel. On ctx.Done the goroutine drains the
// source stream so the upstream provider's blocked send can exit cleanly.
func CheckFirstStreamChunkForError(
	ctx context.Context,
	stream chan *schemas.BifrostStreamChunk,
) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	firstChunk, ok := <-stream
	if !ok {
		// Channel closed immediately (empty stream) — return nil so callers
		// can distinguish this from a live stream channel.
		done := make(chan struct{})
		close(done)
		return nil, done, nil
	}

	buffered := []*schemas.BifrostStreamChunk{firstChunk}
	for {
		if streamChunkError(buffered[len(buffered)-1]) != nil {
			done := drainStream(stream)
			return nil, done, buffered[len(buffered)-1].BifrostError
		}
		if streamChunkStartsOutput(buffered[len(buffered)-1]) {
			break
		}
		next, ok := <-stream
		if !ok {
			done := make(chan struct{})
			wrapped := make(chan *schemas.BifrostStreamChunk, len(buffered))
			for _, chunk := range buffered {
				wrapped <- chunk
			}
			close(wrapped)
			close(done)
			return wrapped, done, nil
		}
		buffered = append(buffered, next)
	}

	// First output is valid data — wrap channel to re-inject buffered pre-output chunks.
	done := make(chan struct{})
	wrapped := make(chan *schemas.BifrostStreamChunk, max(cap(stream), len(buffered), 1))
	for _, chunk := range buffered {
		wrapped <- chunk
	}
	go func() {
		defer close(done)
		defer close(wrapped)
		for chunk := range stream {
			select {
			case wrapped <- chunk:
			case <-ctx.Done():
				// Consumer abandoned the wrapped channel. Drain the source so the
				// provider's blocked send unblocks and its goroutine can exit.
				for range stream {
				}
				return
			}
		}
	}()
	return wrapped, done, nil
}

func streamChunkError(chunk *schemas.BifrostStreamChunk) *schemas.BifrostError {
	if chunk == nil || chunk.BifrostError == nil || chunk.BifrostError.Error == nil {
		return nil
	}
	if chunk.BifrostError.Error.Message != "" || chunk.BifrostError.Error.Code != nil || chunk.BifrostError.Error.Type != nil {
		return chunk.BifrostError
	}
	return nil
}

func streamChunkStartsOutput(chunk *schemas.BifrostStreamChunk) bool {
	if chunk == nil {
		return false
	}
	if response := chunk.BifrostResponsesStreamResponse; response != nil {
		switch response.Type {
		case schemas.ResponsesStreamResponseTypePing,
			schemas.ResponsesStreamResponseTypeCreated,
			schemas.ResponsesStreamResponseTypeInProgress:
			return false
		}
	}
	return true
}

func drainStream(stream chan *schemas.BifrostStreamChunk) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream {
		}
	}()
	return done
}
