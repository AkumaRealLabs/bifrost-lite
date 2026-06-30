import { Button } from "@/components/ui/button";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { DefaultNetworkConfig } from "@/lib/constants/config";
import { cleanLitePathOverrides, DefaultLiteAllowedRequests, LiteBaseProviders } from "@/lib/constants/lite";
import { getErrorMessage, useCreateProviderMutation } from "@/lib/store";
import { BaseProvider, ModelProviderName } from "@/lib/types/config";
import { allowedRequestsSchema } from "@/lib/types/schemas";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";
import { AllowedRequestsFields } from "../fragments/allowedRequestsFields";

const liteBaseProviderSet = new Set<string>(LiteBaseProviders);

const formSchema = z.object({
	name: z.string().min(1, "Provider 名称必填"),
	baseFormat: z.string().min(1, "请选择基础格式").refine((value) => liteBaseProviderSet.has(value), "请选择 Lite 支持的基础格式"),
	base_url: z.string().min(1, "Base URL 必填").url("请输入有效的 URL"),
	allowed_requests: allowedRequestsSchema,
	request_path_overrides: z.record(z.string(), z.string().optional()).optional(),
	is_key_less: z.boolean().optional(),
	allow_private_network: z.boolean().optional(),
});

type FormData = z.infer<typeof formSchema>;

export interface AddCustomProviderSheetContentProps {
	show?: boolean;
	onSave: (id: string) => void;
	onClose: () => void;
}

interface Props extends AddCustomProviderSheetContentProps {
	show: boolean;
}

export function AddCustomProviderSheetContent({ show = true, onClose, onSave }: AddCustomProviderSheetContentProps) {
	const hasProviderCreateAccess = useRbac(RbacResource.ModelProvider, RbacOperation.Create);
	const [addProvider, { isLoading: isAddingProvider }] = useCreateProviderMutation();
	const form = useForm<FormData>({
		resolver: zodResolver(formSchema),
		defaultValues: {
			name: "",
			baseFormat: "",
			base_url: "",
			allowed_requests: { ...DefaultLiteAllowedRequests },
			request_path_overrides: undefined,
			is_key_less: false,
			allow_private_network: false,
		},
	});

	useEffect(() => {
		if (show) {
			form.clearErrors();
		}
	}, [show]);

	const onSubmit = (data: FormData) => {
		const payload = {
			provider: data.name as ModelProviderName,
			custom_provider_config: {
				base_provider_type: data.baseFormat as BaseProvider,
				allowed_requests: data.allowed_requests,
				request_path_overrides: cleanLitePathOverrides(data.request_path_overrides),
				is_key_less: data.is_key_less ?? false,
			},
			network_config: {
				base_url: data.base_url,
				allow_private_network: data.allow_private_network ?? false,
				default_request_timeout_in_seconds: DefaultNetworkConfig.default_request_timeout_in_seconds,
				max_retries: 0,
				retry_backoff_initial: 500,
				retry_backoff_max: 5000,
			},
		};

		addProvider(payload)
			.unwrap()
			.then((provider) => {
				onSave(provider.name);
				form.reset();
			})
			.catch((err) => {
				toast.error("添加 Provider 失败", {
					description: getErrorMessage(err),
				});
			});
	};

	const baseFormat = form.watch("baseFormat") as BaseProvider;
	const isKeyLessDisabled = baseFormat === "bedrock";

	return (
		<>
			<SheetHeader className="flex shrink-0 flex-col items-start px-8 py-4" headerClassName="mb-0 sticky -top-4 bg-card z-10">
				<SheetTitle>添加自定义 Provider</SheetTitle>
				<SheetDescription>填写自定义 Provider 的连接信息。</SheetDescription>
			</SheetHeader>
			<Form {...form}>
				<form onSubmit={form.handleSubmit(onSubmit)} className="flex flex-col">
					<div className="min-h-0 flex-1 space-y-4 px-8 pb-4">
						<FormField
							control={form.control}
							name="name"
							render={({ field }) => (
								<FormItem className="flex flex-col gap-3">
									<FormLabel className="text-right">名称</FormLabel>
									<div className="col-span-3">
										<FormControl>
											<Input placeholder="Provider 名称" data-testid="custom-provider-name" disabled={!hasProviderCreateAccess} {...field} />
										</FormControl>
										<FormMessage />
									</div>
								</FormItem>
							)}
						/>
						<FormField
							control={form.control}
							name="baseFormat"
							render={({ field }) => (
								<FormItem className="flex flex-col gap-3">
									<FormLabel>基础格式</FormLabel>
									<div>
										<FormControl>
											<Select onValueChange={field.onChange} value={field.value} disabled={!hasProviderCreateAccess}>
												<SelectTrigger className="w-full" data-testid="base-provider-select">
													<SelectValue placeholder="选择基础格式" />
												</SelectTrigger>
												<SelectContent>
													<SelectItem value="openai">OpenAI</SelectItem>
													<SelectItem value="anthropic">Anthropic</SelectItem>
													<SelectItem value="gemini">Gemini</SelectItem>
													<SelectItem value="cohere">Cohere</SelectItem>
													<SelectItem value="bedrock">AWS Bedrock</SelectItem>
													<SelectItem value="huggingface">HuggingFace</SelectItem>
													<SelectItem value="replicate">Replicate</SelectItem>
												</SelectContent>
											</Select>
										</FormControl>
										<FormMessage />
									</div>
								</FormItem>
							)}
						/>
						<FormField
							control={form.control}
							name="base_url"
							render={({ field }) => (
								<FormItem className="flex flex-col gap-3">
									<FormLabel>Base URL</FormLabel>
									<div>
										<FormControl>
											<Input
												placeholder={"https://api.your-provider.com"}
												data-testid="base-url-input"
												disabled={!hasProviderCreateAccess}
												{...field}
												value={field.value || ""}
											/>
										</FormControl>
										<FormMessage />
									</div>
								</FormItem>
							)}
						/>
						<FormField
							control={form.control}
							name="allow_private_network"
							render={({ field }) => (
								<FormItem>
									<div className="flex items-center justify-between space-x-2 rounded-lg border p-3">
										<div className="space-y-0.5">
											<label htmlFor="allow-private-network" className="text-sm font-medium">
												允许私有网络
											</label>
											<p className="text-muted-foreground text-sm">
												允许连接私有网络 IP（例如 192.168.x.x、10.x.x.x）。链路本地地址仍会被阻止。
											</p>
										</div>
										<Switch
											id="allow-private-network"
											size="md"
											checked={field.value}
											onCheckedChange={field.onChange}
											disabled={!hasProviderCreateAccess}
											data-testid="custom-provider-allow-private-network-switch"
										/>
									</div>
								</FormItem>
							)}
						/>
						{!isKeyLessDisabled && (
							<FormField
								control={form.control}
								name="is_key_less"
								render={({ field }) => (
									<FormItem>
										<div className="flex items-center justify-between space-x-2 rounded-lg border p-3">
											<div className="space-y-0.5">
												<label htmlFor="drop-excess-requests" className="text-sm font-medium">
													无需 Key？
												</label>
												<p className="text-muted-foreground text-sm">此自定义 Provider 是否无需配置 Key。</p>
											</div>
											<Switch
												id="drop-excess-requests"
												size="md"
												checked={field.value}
												onCheckedChange={field.onChange}
												disabled={!hasProviderCreateAccess}
												data-testid="custom-provider-keyless-switch"
											/>
										</div>
									</FormItem>
								)}
							/>
						)}
						{/* Allowed Requests Configuration */}
						<AllowedRequestsFields
							control={form.control}
							providerType={form.watch("baseFormat") as BaseProvider}
							disabled={!hasProviderCreateAccess}
						/>
					</div>
					<div className="bg-card sticky bottom-0 ml-auto flex w-full flex-row gap-2 border-t px-8 py-4">
						<Button type="button" variant="outline" onClick={onClose} className="ml-auto" data-testid="custom-provider-cancel-btn">
							取消
						</Button>
						<Button type="submit" isLoading={isAddingProvider} disabled={!hasProviderCreateAccess} data-testid="custom-provider-save-btn">
							添加
						</Button>
					</div>
				</form>
			</Form>
		</>
	);
}

export default function AddCustomProviderSheet(props: Props) {
	return (
		<Sheet open={props.show} onOpenChange={(open) => !open && props.onClose()}>
			<SheetContent data-testid="custom-provider-sheet" className="p-0 pt-4">
				<AddCustomProviderSheetContent {...props} />
			</SheetContent>
		</Sheet>
	);
}
