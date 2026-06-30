package lib

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
)

func validLiteCustomProviderConfig() configstore.ProviderConfig {
	return configstore.ProviderConfig{
		CustomProviderConfig: &schemas.CustomProviderConfig{
			BaseProviderType: schemas.OpenAI,
			AllowedRequests: &schemas.AllowedRequests{
				ListModels:            true,
				ChatCompletion:        true,
				ChatCompletionStream:  true,
				Responses:             true,
				ResponsesStream:       true,
				ImageGeneration:       true,
				ImageGenerationStream: true,
				ImageEdit:             true,
				ImageEditStream:       true,
			},
		},
	}
}

func TestValidateCustomProviderLiteAcceptsImageRequests(t *testing.T) {
	if err := ValidateCustomProvider(validLiteCustomProviderConfig(), "my_openai"); err != nil {
		t.Fatalf("expected Lite custom provider to pass, got: %v", err)
	}
}

func TestValidateCustomProviderLiteRejectsStandardProviderName(t *testing.T) {
	err := ValidateCustomProvider(validLiteCustomProviderConfig(), schemas.OpenAI)
	if err == nil || !strings.Contains(err.Error(), "standard providers") {
		t.Fatalf("expected standard provider name rejection, got: %v", err)
	}
}

func TestValidateCustomProviderLiteRequiresCustomProviderConfig(t *testing.T) {
	err := ValidateCustomProvider(configstore.ProviderConfig{}, "my_openai")
	if err == nil || !strings.Contains(err.Error(), "custom_provider_config is required") {
		t.Fatalf("expected missing custom_provider_config rejection, got: %v", err)
	}
}

func TestValidateCustomProviderLiteRequiresAllowedRequests(t *testing.T) {
	cfg := validLiteCustomProviderConfig()
	cfg.CustomProviderConfig.AllowedRequests = nil
	err := ValidateCustomProvider(cfg, "my_openai")
	if err == nil || !strings.Contains(err.Error(), "allowed_requests is required") {
		t.Fatalf("expected missing allowed_requests rejection, got: %v", err)
	}
}

func TestValidateCustomProviderLiteRejectsNonLiteAllowedRequest(t *testing.T) {
	cfg := validLiteCustomProviderConfig()
	cfg.CustomProviderConfig.AllowedRequests.Embedding = true
	err := ValidateCustomProvider(cfg, "my_openai")
	if err == nil || !strings.Contains(err.Error(), "allowed_requests.embedding is not supported in Lite") {
		t.Fatalf("expected embedding rejection, got: %v", err)
	}
}

func TestValidateCustomProviderLiteRejectsNonLitePathOverride(t *testing.T) {
	cfg := validLiteCustomProviderConfig()
	cfg.CustomProviderConfig.RequestPathOverrides = map[schemas.RequestType]string{
		schemas.EmbeddingRequest: "/v1/embeddings",
	}
	err := ValidateCustomProvider(cfg, "my_openai")
	if err == nil || !strings.Contains(err.Error(), "request_path_overrides.embedding is not supported in Lite") {
		t.Fatalf("expected embedding path override rejection, got: %v", err)
	}
}
