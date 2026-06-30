import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getErrorMessage, useGetCoreConfigQuery, useUpdateCoreConfigMutation } from "@/lib/store";
import { DefaultCoreConfig } from "@/lib/types/config";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { useEffect, useMemo } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

interface ModelSettingsFormData {
	pricing_datasheet_url: string;
	pricing_sync_interval_hours: number;
	model_parameters_url: string;
	routing_chain_max_depth: number;
}

export default function ModelSettingsView() {
	const hasSettingsUpdateAccess = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data: bifrostConfig } = useGetCoreConfigQuery({ fromDB: true });
	const frameworkConfig = bifrostConfig?.framework_config;
	const clientConfig = bifrostConfig?.client_config;
	const [updateCoreConfig, { isLoading }] = useUpdateCoreConfigMutation();

	const {
		register,
		handleSubmit,
		formState: { errors, isDirty },
		reset,
		watch,
	} = useForm<ModelSettingsFormData>({
		defaultValues: {
			pricing_datasheet_url: "",
			pricing_sync_interval_hours: 24,
			model_parameters_url: "",
			routing_chain_max_depth: DefaultCoreConfig.routing_chain_max_depth,
		},
	});

	const formValues = watch();

	useEffect(() => {
		if (!bifrostConfig || isDirty) return;
		reset({
			pricing_datasheet_url: frameworkConfig?.pricing_url || "",
			pricing_sync_interval_hours: Math.round((frameworkConfig?.pricing_sync_interval ?? 0) / 3600) || 24,
			model_parameters_url: frameworkConfig?.model_parameters_url || "",
			routing_chain_max_depth: clientConfig?.routing_chain_max_depth ?? DefaultCoreConfig.routing_chain_max_depth,
		});
	}, [
		frameworkConfig?.pricing_url,
		frameworkConfig?.pricing_sync_interval,
		frameworkConfig?.model_parameters_url,
		clientConfig?.routing_chain_max_depth,
		isDirty,
		reset,
	]);

	const hasChanges = useMemo(() => {
		if (!bifrostConfig || !isDirty) return false;
		const serverUrl = frameworkConfig?.pricing_url || "";
		const serverInterval = Math.round((frameworkConfig?.pricing_sync_interval ?? 0) / 3600);
		const serverModelParamsUrl = frameworkConfig?.model_parameters_url || "";
		const serverDepth = clientConfig?.routing_chain_max_depth ?? DefaultCoreConfig.routing_chain_max_depth;
		return (
			formValues.pricing_datasheet_url !== serverUrl ||
			formValues.pricing_sync_interval_hours !== serverInterval ||
			formValues.model_parameters_url !== serverModelParamsUrl ||
			formValues.routing_chain_max_depth !== serverDepth
		);
	}, [bifrostConfig, frameworkConfig, clientConfig, formValues, isDirty]);

	const onSubmit = async (data: ModelSettingsFormData) => {
		try {
			await updateCoreConfig({
				...bifrostConfig!,
				framework_config: {
					...frameworkConfig,
					id: bifrostConfig?.framework_config.id || 0,
					pricing_url: data.pricing_datasheet_url,
					pricing_sync_interval: data.pricing_sync_interval_hours * 3600,
					model_parameters_url: data.model_parameters_url,
				},
				client_config: {
					...clientConfig!,
					routing_chain_max_depth: data.routing_chain_max_depth,
				},
			}).unwrap();
			toast.success("模型设置已更新");
			reset(data);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<div className="mx-auto w-full max-w-7xl space-y-4" data-testid="model-settings-view">
			<form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
				<div>
				<h2 className="text-lg font-semibold tracking-tight">模型设置</h2>
				<p className="text-muted-foreground text-sm">配置价格和路由行为。</p>
				</div>

				<div className="space-y-4">
					{/* Pricing Datasheet URL */}
					<div className="space-y-2 rounded-sm border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="pricing-datasheet-url">价格表 URL</Label>
							<p className="text-muted-foreground text-sm">自定义价格表的 URL。留空则使用默认价格。</p>
						</div>
						<Input
							id="pricing-datasheet-url"
							type="text"
							placeholder="https://example.com/pricing.json"
							data-testid="pricing-datasheet-url-input"
							{...register("pricing_datasheet_url", {
								validate: {
									checkIfValidUrl: (value) => {
										if (!value) return true;
										return (
											value.startsWith("http://") ||
											value.startsWith("https://") ||
											value.startsWith("file://") ||
										"URL 必须以 http://、https:// 或 file:// 开头"
										);
									},
								},
							})}
							className={errors.pricing_datasheet_url ? "border-destructive" : ""}
						/>
						{errors.pricing_datasheet_url && <p className="text-destructive text-sm">{errors.pricing_datasheet_url.message}</p>}
					</div>

					{/* Model Parameters URL */}
					<div className="space-y-2 rounded-sm border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="model-parameters-url">模型参数 URL</Label>
							<p className="text-muted-foreground text-sm">自定义模型参数表的 URL。留空则使用默认值。</p>
						</div>
						<Input
							id="model-parameters-url"
							type="text"
							placeholder="https://example.com/model-parameters.json"
							data-testid="model-parameters-url-input"
							{...register("model_parameters_url", {
								validate: {
									checkIfValidUrl: (value) => {
										if (!value) return true;
										return (
											value.startsWith("http://") ||
											value.startsWith("https://") ||
											value.startsWith("file://") ||
										"URL 必须以 http://、https:// 或 file:// 开头"
										);
									},
								},
							})}
							className={errors.model_parameters_url ? "border-destructive" : ""}
						/>
						{errors.model_parameters_url && <p className="text-destructive text-sm">{errors.model_parameters_url.message}</p>}
					</div>

					{/* Pricing Sync Interval */}
					<div className="space-y-2 rounded-sm border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="pricing-sync-interval">价格同步间隔（小时）</Label>
							<p className="text-muted-foreground text-sm">从价格表 URL 同步价格数据的频率。</p>
						</div>
						<Input
							id="pricing-sync-interval"
							type="number"
							data-testid="pricing-sync-interval-input"
							className={errors.pricing_sync_interval_hours ? "border-destructive" : ""}
							{...register("pricing_sync_interval_hours", {
								required: "价格同步间隔不能为空",
								min: { value: 1, message: "同步间隔至少为 1 小时" },
								max: { value: 8760, message: "同步间隔不能超过 8760 小时（一年）" },
								valueAsNumber: true,
							})}
						/>
						{errors.pricing_sync_interval_hours && <p className="text-destructive text-sm">{errors.pricing_sync_interval_hours.message}</p>}
					</div>

					{/* Routing Chain Max Depth */}
					<div className="flex items-center justify-between rounded-sm border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="routing-chain-max-depth">路由链最大深度</Label>
							<p className="text-muted-foreground text-sm">
								每个请求最多允许串联多少层路由规则评估，避免循环规则导致死循环。
							</p>
						</div>
						<Input
							id="routing-chain-max-depth"
							type="number"
							className={`w-24 ${errors.routing_chain_max_depth ? "border-destructive" : ""}`}
							data-testid="routing-chain-max-depth-input"
							{...register("routing_chain_max_depth", {
								required: "路由链最大深度不能为空",
								min: { value: 1, message: "至少为 1" },
								max: { value: 100, message: "不能超过 100" },
								valueAsNumber: true,
							})}
						/>
					</div>
					{errors.routing_chain_max_depth && <p className="text-destructive text-sm">{errors.routing_chain_max_depth.message}</p>}
				</div>

				<div className="flex justify-end gap-2 pt-2">
					<Button type="submit" disabled={!hasChanges || isLoading || !hasSettingsUpdateAccess} data-testid="model-settings-save-btn">
						{isLoading ? "正在保存..." : "保存修改"}
					</Button>
				</div>
			</form>
		</div>
	);
}
