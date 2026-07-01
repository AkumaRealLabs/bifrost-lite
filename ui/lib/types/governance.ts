import { ModelProviderName, RequestType } from "./config";

export interface DBKey {
	key_id: string;
	name: string;
	provider_id: string;
	models: string[];
	provider: ModelProviderName;
}

export interface RedactedDBKey {
	id: string;
	name: string;
	models: string[];
	weight: number;
}

export interface VirtualKey {
	id: string;
	name: string;
	value: string;
	description?: string;
	provider_configs?: VirtualKeyProviderConfig[];
	is_active: boolean;
	created_at: string;
	updated_at: string;
	config_hash?: string;
	system_pool?: string;
	pool_rule?: string;
	provider_count?: number;
	healthy_provider_count?: number;
	pool_providers?: string[];
}

export interface VirtualKeyProviderConfig {
	id?: number;
	provider: string;
	weight: number | null;
	allowed_models: string[];
	blacklisted_models: string[];
	allow_all_keys: boolean;
	keys?: DBKey[];
}

export interface VirtualKeyProviderConfigRequest {
	provider: string;
	weight?: number | null;
	allowed_models?: string[];
	blacklisted_models?: string[];
	budgets?: unknown[];
	rate_limit?: Record<string, never>;
	key_ids?: string[];
}

export interface VirtualKeyProviderConfigUpdateRequest extends VirtualKeyProviderConfigRequest {
	id?: number;
}

export interface CreateVirtualKeyRequest {
	name: string;
	description?: string;
	provider_configs?: VirtualKeyProviderConfigRequest[];
	is_active?: boolean;
}

export interface UpdateVirtualKeyRequest {
	name?: string;
	description?: string;
	provider_configs?: VirtualKeyProviderConfigUpdateRequest[];
	is_active?: boolean;
	budgets?: unknown[];
	rate_limit?: Record<string, never>;
	calendar_aligned?: boolean;
}

export interface BulkRotateVirtualKeysRequest {
	ids: string[];
}

export interface BulkRotateVirtualKeysResponse {
	message: string;
	virtual_keys: VirtualKey[];
	errors?: Record<string, string>;
}

export interface GetVirtualKeysParams {
	limit?: number;
	offset?: number;
	search?: string;
	sort_by?: "name" | "created_at" | "status";
	order?: "asc" | "desc";
	export?: boolean;
}

export interface GetVirtualKeysResponse {
	virtual_keys: VirtualKey[];
	count: number;
	total_count: number;
	limit: number;
	offset: number;
}

export type PricingOverrideScopeKind =
	| "global"
	| "provider"
	| "provider_key"
	| "virtual_key"
	| "virtual_key_provider"
	| "virtual_key_provider_key";
export type PricingOverrideMatchType = "exact" | "wildcard";

export interface PricingOverridePatch {
	input_cost_per_token?: number;
	output_cost_per_token?: number;
	input_cost_per_token_batches?: number;
	output_cost_per_token_batches?: number;
	input_cost_per_token_priority?: number;
	output_cost_per_token_priority?: number;
	input_cost_per_token_flex?: number;
	output_cost_per_token_flex?: number;
	input_cost_per_token_fast?: number;
	output_cost_per_token_fast?: number;
	input_cost_per_token_above_128k_tokens?: number;
	output_cost_per_token_above_128k_tokens?: number;
	input_cost_per_token_above_200k_tokens?: number;
	input_cost_per_token_above_200k_tokens_priority?: number;
	output_cost_per_token_above_200k_tokens?: number;
	output_cost_per_token_above_200k_tokens_priority?: number;
	input_cost_per_token_above_272k_tokens?: number;
	input_cost_per_token_above_272k_tokens_priority?: number;
	output_cost_per_token_above_272k_tokens?: number;
	output_cost_per_token_above_272k_tokens_priority?: number;
	cache_creation_input_token_cost?: number;
	cache_read_input_token_cost?: number;
	cache_creation_input_token_cost_above_200k_tokens?: number;
	cache_read_input_token_cost_above_200k_tokens?: number;
	cache_creation_input_token_cost_above_1hr?: number;
	cache_creation_input_token_cost_above_1hr_above_200k_tokens?: number;
	cache_read_input_token_cost_priority?: number;
	cache_read_input_token_cost_flex?: number;
	cache_read_input_token_cost_above_200k_tokens_priority?: number;
	cache_read_input_token_cost_above_272k_tokens?: number;
	cache_read_input_token_cost_above_272k_tokens_priority?: number;
	search_context_cost_per_query?: number;
	code_interpreter_cost_per_session?: number;
	input_cost_per_character?: number;
	input_cost_per_audio_token?: number;
	input_cost_per_audio_per_second?: number;
	input_cost_per_audio_per_second_above_128k_tokens?: number;
	input_cost_per_second?: number;
	output_cost_per_audio_token?: number;
	output_cost_per_second?: number;
	cache_creation_input_audio_token_cost?: number;
	input_cost_per_image_token?: number;
	input_cost_per_image?: number;
	input_cost_per_image_above_128k_tokens?: number;
	input_cost_per_pixel?: number;
	output_cost_per_image_token?: number;
	output_cost_per_image?: number;
	output_cost_per_pixel?: number;
	output_cost_per_image_premium_image?: number;
	output_cost_per_image_above_512_and_512_pixels?: number;
	output_cost_per_image_above_512_and_512_pixels_and_premium_image?: number;
	output_cost_per_image_above_1024_and_1024_pixels?: number;
	output_cost_per_image_above_1024_and_1024_pixels_and_premium_image?: number;
	output_cost_per_image_low_quality?: number;
	output_cost_per_image_medium_quality?: number;
	output_cost_per_image_high_quality?: number;
	output_cost_per_image_auto_quality?: number;
	cache_read_input_image_token_cost?: number;
	input_cost_per_video_per_second?: number;
	input_cost_per_video_per_second_above_128k_tokens?: number;
	output_cost_per_video_per_second?: number;
	ocr_cost_per_page?: number;
	annotation_cost_per_page?: number;
}

export interface PricingOverride {
	id: string;
	name: string;
	scope_kind: PricingOverrideScopeKind;
	virtual_key_id?: string;
	provider_id?: string;
	provider_key_id?: string;
	match_type: PricingOverrideMatchType;
	pattern: string;
	request_types?: RequestType[];
	pricing_patch: string;
	config_hash?: string;
	created_at: string;
	updated_at: string;
}

export interface CreatePricingOverrideRequest {
	name: string;
	scope_kind: PricingOverrideScopeKind;
	virtual_key_id?: string;
	provider_id?: string;
	provider_key_id?: string;
	match_type: PricingOverrideMatchType;
	pattern: string;
	request_types: RequestType[];
	patch?: PricingOverridePatch;
}

export interface UpdatePricingOverrideRequest {
	name?: string;
	scope_kind?: PricingOverrideScopeKind;
	virtual_key_id?: string;
	provider_id?: string;
	provider_key_id?: string;
	match_type?: PricingOverrideMatchType;
	pattern?: string;
	request_types?: RequestType[];
	patch?: PricingOverridePatch;
}

export interface GetPricingOverridesResponse {
	pricing_overrides: PricingOverride[];
	count: number;
	total_count: number;
	limit: number;
	offset: number;
}
