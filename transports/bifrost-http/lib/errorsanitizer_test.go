package lib

import (
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestSanitizeBifrostErrorForClientHidesInternalDetails(t *testing.T) {
	statusCode := fasthttp.StatusInternalServerError
	err := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: "failed to create customer: pq: duplicate key value violates unique constraint users_email_key",
			Error:   errors.New("goroutine 1 [running]:\nmain.handler\n\t/app/server.go:42"),
			Param:   "users_email_key",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized == err {
		t.Fatal("expected sanitizer to return a copy")
	}
	if sanitized.Error.Message != ClientSafeInternalErrorMessage {
		t.Fatalf("expected generic message, got %q", sanitized.Error.Message)
	}
	if sanitized.Error.Error != nil {
		t.Fatalf("expected sensitive nested error to be removed, got %v", sanitized.Error.Error)
	}
	if sanitized.Error.Param != nil {
		t.Fatalf("expected param to be removed, got %v", sanitized.Error.Param)
	}
	if err.Error.Message == ClientSafeInternalErrorMessage || err.Error.Error == nil || err.Error.Param == nil {
		t.Fatal("expected original error to remain unchanged")
	}
}

func TestSanitizeBifrostErrorForClientPreservesClientValidationMessage(t *testing.T) {
	statusCode := fasthttp.StatusBadRequest
	err := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "model is required",
			Error:   errors.New("missing model"),
			Param:   "model",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized.Error.Message != "model is required" {
		t.Fatalf("expected validation message to be preserved, got %q", sanitized.Error.Message)
	}
	if sanitized.Error.Param != "model" {
		t.Fatalf("expected param to be preserved, got %v", sanitized.Error.Param)
	}
	if sanitized.Error.Error == nil {
		t.Fatal("expected non-sensitive nested error to be preserved")
	}
}

func TestSanitizeBifrostErrorForClientHidesInternalRoutingConfigDetails(t *testing.T) {
	tests := []string{
		"no keys found for provider: provider-alpha and model: gpt-5.5",
		"no keys found that support model: gpt-5.5",
	}

	for _, message := range tests {
		t.Run(message, func(t *testing.T) {
			err := &schemas.BifrostError{
				Error: &schemas.ErrorField{
					Message: message,
					Error:   errors.New("inner detail"),
					Param:   "gpt-5.5",
				},
			}

			sanitized := SanitizeBifrostErrorForClient(err)

			if sanitized == err {
				t.Fatal("expected sanitizer to return a copy")
			}
			if sanitized.Error.Message != ClientSafeInternalErrorMessage {
				t.Fatalf("expected generic message, got %q", sanitized.Error.Message)
			}
			if sanitized.Error.Error != nil {
				t.Fatalf("expected nested error to be removed, got %v", sanitized.Error.Error)
			}
			if sanitized.Error.Param != nil {
				t.Fatalf("expected param to be removed, got %v", sanitized.Error.Param)
			}
			if err.Error.Message != message || err.Error.Error == nil || err.Error.Param == nil {
				t.Fatal("expected original error to remain unchanged")
			}
		})
	}
}

func TestSanitizeBifrostErrorForClientPreservesNonSensitiveServerMessage(t *testing.T) {
	statusCode := fasthttp.StatusInternalServerError
	err := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "failed to reload config",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized.Error.Message != "failed to reload config" {
		t.Fatalf("expected non-sensitive server message to be preserved, got %q", sanitized.Error.Message)
	}
}

func TestSanitizeBifrostErrorForClientHidesRoutingFields(t *testing.T) {
	aliasName := "internal-alias-name"
	err := &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Message: "Concurrency limit exceeded for account, please retry later",
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			Provider:          "provider-alpha",
			ResolvedModelUsed: "internal-resolved-model",
			RoutingInfo: schemas.RoutingInfo{
				Provider: "provider-alpha",
				Model:    "internal-routing-model",
				Key:      "key-prod-01",
				ResolvedKeyAlias: &schemas.ResolvedKeyAlias{
					ModelID:   "upstream-resolved-model",
					ModelName: &aliasName,
				},
			},
			KeyStatuses: []schemas.KeyStatus{{
				KeyID:    "key-status-01",
				Provider: "provider-beta",
			}},
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)
	if sanitized.ExtraFields.Provider != "" {
		t.Fatalf("expected provider to be cleared, got %q", sanitized.ExtraFields.Provider)
	}
	if sanitized.ExtraFields.ResolvedModelUsed != "" {
		t.Fatalf("expected resolved model to be cleared, got %q", sanitized.ExtraFields.ResolvedModelUsed)
	}
	if sanitized.ExtraFields.RoutingInfo.Key != "" || sanitized.ExtraFields.RoutingInfo.ResolvedKeyAlias != nil {
		t.Fatalf("expected routing info to be cleared, got %+v", sanitized.ExtraFields.RoutingInfo)
	}
	if len(sanitized.ExtraFields.KeyStatuses) != 0 {
		t.Fatalf("expected key statuses to be cleared, got %+v", sanitized.ExtraFields.KeyStatuses)
	}
	if err.ExtraFields.Provider == "" || err.ExtraFields.RoutingInfo.Key == "" || err.ExtraFields.RoutingInfo.ResolvedKeyAlias == nil || len(err.ExtraFields.KeyStatuses) == 0 {
		t.Fatal("expected original error to remain unchanged")
	}

	data, marshalErr := schemas.MarshalSorted(sanitized)
	if marshalErr != nil {
		t.Fatalf("failed to marshal sanitized error: %v", marshalErr)
	}
	body := string(data)
	for _, sensitive := range []string{"provider-alpha", "provider-beta", "key-prod-01", "key-status-01", "internal-resolved-model", "upstream-resolved-model", aliasName} {
		if strings.Contains(body, sensitive) {
			t.Fatalf("sanitized JSON leaked %q: %s", sensitive, body)
		}
	}
}
