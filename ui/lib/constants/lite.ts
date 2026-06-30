export const LiteBaseProviders = ["openai", "anthropic", "bedrock", "cohere", "gemini", "huggingface", "replicate"] as const;

export const LiteRequestTypes = [
	"list_models",
	"chat_completion",
	"chat_completion_stream",
	"responses",
	"responses_stream",
	"image_generation",
	"image_generation_stream",
	"image_edit",
	"image_edit_stream",
] as const;

export type LiteRequestType = (typeof LiteRequestTypes)[number];

export const LiteRequestTypeLabels: Record<LiteRequestType, string> = {
	list_models: "模型列表",
	chat_completion: "聊天补全",
	chat_completion_stream: "聊天补全（流式）",
	responses: "Responses",
	responses_stream: "Responses（流式）",
	image_generation: "图像生成",
	image_generation_stream: "图像生成（流式）",
	image_edit: "图像编辑",
	image_edit_stream: "图像编辑（流式）",
};

export const DefaultLiteAllowedRequests = Object.fromEntries(LiteRequestTypes.map((key) => [key, true])) as Record<
	LiteRequestType,
	boolean
>;

export function toLiteAllowedRequests(value?: Partial<Record<string, boolean>>): Record<LiteRequestType, boolean> {
	return Object.fromEntries(LiteRequestTypes.map((key) => [key, value?.[key] ?? true])) as Record<LiteRequestType, boolean>;
}

export function cleanLitePathOverrides(overrides?: Record<string, string | undefined>) {
	if (!overrides) return undefined;
	const allowed = new Set<string>(LiteRequestTypes);
	const entries = Object.entries(overrides)
		.filter(([key]) => allowed.has(key))
		.map(([key, value]) => [key, value?.trim()])
		.filter(([, value]) => value);
	return entries.length ? (Object.fromEntries(entries) as Record<string, string>) : undefined;
}
