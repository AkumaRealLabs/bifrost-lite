import { FormControl, FormField, FormItem, FormLabel } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { LiteRequestType, LiteRequestTypeLabels, LiteRequestTypes } from "@/lib/constants/lite";
import { BaseProvider } from "@/lib/types/config";
import { isRequestTypeDisabled } from "@/lib/utils/validation";
import { Settings2 } from "lucide-react";
import { useEffect, useMemo } from "react";
import { Control, useFormContext } from "react-hook-form";

interface AllowedRequestsFieldsProps {
	control: Control<any>;
	namePrefix?: string;
	pathOverridesPrefix?: string;
	providerType?: BaseProvider;
	disabled?: boolean;
}

// Provider-specific endpoint paths
const ProviderEndpoints: Partial<Record<BaseProvider, Partial<Record<LiteRequestType, string>>>> = {
	openai: {
		list_models: "/v1/models",
		chat_completion: "/v1/chat/completions",
		chat_completion_stream: "/v1/chat/completions",
		responses: "/v1/responses",
		responses_stream: "/v1/responses",
		image_generation: "/v1/images/generations",
		image_generation_stream: "/v1/images/generations",
		image_edit: "/v1/images/edits",
		image_edit_stream: "/v1/images/edits",
	},
	anthropic: {
		chat_completion: "/v1/messages",
		chat_completion_stream: "/v1/messages",
		responses: "/v1/messages",
		responses_stream: "/v1/messages",
	},
	cohere: {
		chat_completion: "/v2/chat",
		chat_completion_stream: "/v2/chat",
		responses: "/v2/chat",
		responses_stream: "/v2/chat",
	},
};

// Helper function to get the appropriate placeholder
const getPlaceholder = (providerType: BaseProvider | undefined, requestKey: LiteRequestType): string => {
	if (providerType && ProviderEndpoints[providerType]?.[requestKey]) {
		return ProviderEndpoints[providerType][requestKey]!;
	}
	return ProviderEndpoints["openai"]?.[requestKey] ?? "";
};

const RequestTypes: Array<{ key: LiteRequestType; label: string }> = LiteRequestTypes.map((key) => ({
	key,
	label: LiteRequestTypeLabels[key],
}));

export function AllowedRequestsFields({
	control,
	namePrefix = "allowed_requests",
	pathOverridesPrefix = "request_path_overrides",
	providerType,
	disabled = false,
}: AllowedRequestsFieldsProps) {
	const leftColumn = RequestTypes.slice(0, RequestTypes.length / 2);
	const rightColumn = RequestTypes.slice(RequestTypes.length / 2);
	const { setValue } = useFormContext();

	// Reset disabled fields when providerType changes
	useEffect(() => {
		RequestTypes.forEach(({ key }) => {
			const fieldName = `${namePrefix}.${key}`;
			setValue(fieldName, !isRequestTypeDisabled(providerType, key), { shouldDirty: false });
		});
	}, [providerType, namePrefix, setValue]);

	const isPathOverrideDisabled = useMemo(() => providerType === "gemini" || providerType === "bedrock", [providerType]);

	const renderRequestField = (requestType: { key: LiteRequestType; label: string }) => {
		const isDisabled = isRequestTypeDisabled(providerType, requestType.key);
		const placeholder = getPlaceholder(providerType, requestType.key);

		return (
			<FormField
				key={requestType.key}
				control={control}
				name={`${namePrefix}.${requestType.key}`}
				render={({ field: allowedField }) => (
					<FormItem
						className={`flex flex-row items-center justify-between rounded-lg border p-3 ${isDisabled ? "bg-muted/30 opacity-60" : ""}`}
					>
						<div className="space-y-0.5">
							<FormLabel className={isDisabled ? "cursor-not-allowed" : ""}>{requestType.label}</FormLabel>
						</div>
						<div className="flex items-center gap-2">
							{/* Settings icon for path override - only show when enabled */}
							{allowedField.value && !isDisabled && !isPathOverrideDisabled && !disabled && (
								<FormField
									control={control}
									name={`${pathOverridesPrefix}.${requestType.key}`}
									render={({ field: pathField }) => (
										<Popover>
											<PopoverTrigger asChild>
												<button
													type="button"
													className="text-muted-foreground hover:text-foreground transition-colors"
													aria-label="自定义端点路径"
												>
													<Settings2 className="h-4 w-4" />
												</button>
											</PopoverTrigger>
											<PopoverContent className="w-80" align="end" onOpenAutoFocus={(e) => e.preventDefault()}>
												<div className="space-y-2">
													<h4 className="text-sm font-medium">自定义路径或 URL</h4>
													<p className="text-muted-foreground text-xs">
														可用路径（例如 /v1/chat）或完整 URL（例如 https://api.example.com/chat）覆盖默认 base_url。
													</p>
													<Input placeholder={placeholder} {...pathField} value={pathField.value || ""} className="h-9" />
												</div>
											</PopoverContent>
										</Popover>
									)}
								/>
							)}

							<FormControl>
								{isDisabled ? (
									<TooltipProvider>
										<Tooltip>
											<TooltipTrigger asChild>
												<div>
													<Switch checked={isDisabled ? false : allowedField.value} disabled={true} size="md" />
												</div>
											</TooltipTrigger>
											<TooltipContent>
												<p>{providerType} 不支持此请求类型</p>
											</TooltipContent>
										</Tooltip>
									</TooltipProvider>
								) : (
									<Switch checked={allowedField.value} onCheckedChange={allowedField.onChange} size="md" disabled={disabled} />
								)}
							</FormControl>
						</div>
					</FormItem>
				)}
			/>
		);
	};

	return (
		<div className="space-y-4">
			<div>
				<div className="text-sm font-medium">允许的请求类型</div>
				<p className="text-muted-foreground text-xs">
					选择此自定义 Provider 可处理的请求类型。{" "}
					{!isPathOverrideDisabled ? "点击设置图标可自定义端点路径或使用完整 URL。" : ""}
				</p>
			</div>

			<div className="grid grid-cols-2 gap-4">
				<div className="space-y-3">{leftColumn.map(renderRequestField)}</div>
				<div className="space-y-3">{rightColumn.map(renderRequestField)}</div>
			</div>
		</div>
	);
}
