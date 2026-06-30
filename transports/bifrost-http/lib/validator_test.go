package lib

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// loadLocalSchema reads the local config.schema.json for use in tests,
// avoiding remote fetches during test execution.
func loadLocalSchema(t *testing.T) []byte {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get current file path")
	}
	schemaPath := filepath.Join(filepath.Dir(filename), "..", "..", "config.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("failed to read config.schema.json: %v", err)
	}
	return data
}

func liteCustomProviderConfig(name string) string {
	return `{
		"providers": {
			"` + name + `": {
				"keys": [
					{
						"name": "default",
						"value": "sk-test-key",
						"weight": 1.0,
						"models": ["gpt-4"]
					}
				],
				"custom_provider_config": {
					"base_provider_type": "openai",
					"allowed_requests": {
						"list_models": true,
						"chat_completion": true,
						"chat_completion_stream": true,
						"responses": true,
						"responses_stream": true,
						"image_generation": true,
						"image_generation_stream": true,
						"image_edit": true,
						"image_edit_stream": true
					}
				}
			}
		}
	}`
}

func assertSchemaValid(t *testing.T, config string) {
	t.Helper()
	if err := ValidateConfigSchema([]byte(config), loadLocalSchema(t)); err != nil {
		t.Fatalf("expected config to pass validation, got error: %v", err)
	}
}

func assertSchemaInvalid(t *testing.T, config string) error {
	t.Helper()
	err := ValidateConfigSchema([]byte(config), loadLocalSchema(t))
	if err == nil {
		t.Fatal("expected config to fail validation")
	}
	return err
}

func TestValidateConfigSchema_EmptyObject(t *testing.T) {
	assertSchemaValid(t, `{}`)
}

func TestValidateConfigSchema_InvalidJSON(t *testing.T) {
	err := assertSchemaInvalid(t, `{invalid json`)
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("expected error to mention 'invalid JSON', got: %v", err)
	}
}

func TestValidateConfigSchema_InvalidClientType(t *testing.T) {
	assertSchemaInvalid(t, `{
		"client": {
			"initial_pool_size": "not-a-number"
		}
	}`)
}

func TestValidateConfigSchema_ValidClientConfig(t *testing.T) {
	assertSchemaValid(t, `{
		"client": {
			"initial_pool_size": 500,
			"drop_excess_requests": true,
			"enable_logging": true,
			"log_retention_days": 30,
			"allowed_origins": ["https://example.com"]
		}
	}`)
}

func TestValidateConfigSchema_ValidLiteCustomProvider(t *testing.T) {
	assertSchemaValid(t, liteCustomProviderConfig("my_openai"))
}

func TestValidateConfigSchema_StandardProviderNameRejected(t *testing.T) {
	assertSchemaInvalid(t, liteCustomProviderConfig("openai"))
}

func TestValidateConfigSchema_CustomProviderRequiresConfig(t *testing.T) {
	assertSchemaInvalid(t, `{
		"providers": {
			"my_openai": {
				"keys": [
					{
						"name": "default",
						"value": "sk-test-key",
						"weight": 1.0
					}
				]
			}
		}
	}`)
}

func TestValidateConfigSchema_CustomProviderRequiresAllowedRequests(t *testing.T) {
	assertSchemaInvalid(t, `{
		"providers": {
			"my_openai": {
				"keys": [
					{
						"name": "default",
						"value": "sk-test-key",
						"weight": 1.0
					}
				],
				"custom_provider_config": {
					"base_provider_type": "openai"
				}
			}
		}
	}`)
}

func TestValidateConfigSchema_CustomProviderRejectsUnsupportedBaseProvider(t *testing.T) {
	assertSchemaInvalid(t, `{
		"providers": {
			"my_fireworks": {
				"keys": [
					{
						"name": "default",
						"value": "sk-test-key",
						"weight": 1.0
					}
				],
				"custom_provider_config": {
					"base_provider_type": "fireworks",
					"allowed_requests": {
						"list_models": true,
						"chat_completion": true,
						"chat_completion_stream": true,
						"responses": true,
						"responses_stream": true,
						"image_generation": true,
						"image_generation_stream": true,
						"image_edit": true,
						"image_edit_stream": true
					}
				}
			}
		}
	}`)
}

func TestValidateConfigSchema_CustomProviderRejectsNonLiteRequest(t *testing.T) {
	assertSchemaInvalid(t, `{
		"providers": {
			"my_openai": {
				"keys": [
					{
						"name": "default",
						"value": "sk-test-key",
						"weight": 1.0
					}
				],
				"custom_provider_config": {
					"base_provider_type": "openai",
					"allowed_requests": {
						"chat_completion": true,
						"responses": true,
						"embedding": true
					}
				}
			}
		}
	}`)
}

func TestValidateConfigSchema_CustomProviderRejectsNonLitePathOverride(t *testing.T) {
	assertSchemaInvalid(t, `{
		"providers": {
			"my_openai": {
				"keys": [
					{
						"name": "default",
						"value": "sk-test-key",
						"weight": 1.0
					}
				],
				"custom_provider_config": {
					"base_provider_type": "openai",
					"allowed_requests": {
						"list_models": true,
						"chat_completion": true,
						"chat_completion_stream": true,
						"responses": true,
						"responses_stream": true,
						"image_generation": true,
						"image_generation_stream": true,
						"image_edit": true,
						"image_edit_stream": true
					},
					"request_path_overrides": {
						"embedding": "/v1/embeddings"
					}
				}
			}
		}
	}`)
}

func TestValidateConfigSchema_LiteKeepsPricingOverrides(t *testing.T) {
	assertSchemaValid(t, `{
		"governance": {
			"virtual_keys": [
				{
					"id": "vk-1",
					"name": "Test Key",
					"value": "vk_test123"
				}
			],
			"pricing_overrides": [
				{
					"id": "po-1",
					"name": "Image promo",
					"scope_kind": "global",
					"match_type": "exact",
					"pattern": "gpt-image-2",
					"request_types": ["image_generation"],
					"pricing_patch": "{\"output_cost_per_image\":0.07}"
				}
			]
		}
	}`)
}

func TestValidateConfigSchema_PricingOverridesRejectNonLiteRequest(t *testing.T) {
	assertSchemaInvalid(t, `{
		"governance": {
			"pricing_overrides": [
				{
					"id": "po-1",
					"name": "Embedding",
					"scope_kind": "global",
					"match_type": "exact",
					"pattern": "text-embedding-3-small",
					"request_types": ["embedding"],
					"pricing_patch": "{\"input_cost_per_token\":0.01}"
				}
			]
		}
	}`)
}

func TestValidateConfigSchema_LiteRejectsRetiredGovernanceSurfaces(t *testing.T) {
	cases := map[string]string{
		"budgets": `{
			"governance": {
				"budgets": [
					{
						"id": "budget-1",
						"max_limit": 100.0,
						"reset_duration": "1d"
					}
				]
			}
		}`,
		"rate_limits": `{
			"governance": {
				"rate_limits": [
					{
						"id": "rate-limit-1",
						"token_max_limit": 10000,
						"token_reset_duration": "1h"
					}
				]
			}
		}`,
		"customers": `{
			"governance": {
				"customers": [
					{
						"id": "customer-1",
						"name": "Acme Corp"
					}
				]
			}
		}`,
		"teams": `{
			"governance": {
				"teams": [
					{
						"id": "team-1",
						"name": "Engineering"
					}
				]
				}
			}`,
	}

	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			assertSchemaInvalid(t, config)
		})
	}
}

func TestValidateConfigSchema_LiteRejectsRetiredTopLevelSurfaces(t *testing.T) {
	cases := map[string]string{
		"vector_store": `{
			"vector_store": {
				"enabled": true,
				"type": "redis"
			}
		}`,
		"guardrails_config": `{
			"guardrails_config": {}
		}`,
		"scim_config": `{
			"scim_config": {}
		}`,
	}

	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			assertSchemaInvalid(t, config)
		})
	}
}

func TestValidateConfigSchema_LitePluginSet(t *testing.T) {
	assertSchemaValid(t, `{
		"plugins": [
			{
				"enabled": true,
				"name": "logging",
				"config": {}
			}
		]
	}`)

	for _, pluginName := range []string{"telemetry", "semantic_cache", "otel", "maxim"} {
		t.Run(pluginName, func(t *testing.T) {
			assertSchemaInvalid(t, `{
				"plugins": [
					{
						"enabled": true,
						"name": "`+pluginName+`",
						"config": {}
					}
				]
			}`)
		})
	}
}
