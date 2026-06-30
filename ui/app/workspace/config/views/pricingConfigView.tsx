import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getErrorMessage, useGetCoreConfigQuery, useUpdateCoreConfigMutation } from "@/lib/store";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { useEffect, useMemo } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";

interface PricingFormData {
	pricing_datasheet_url: string;
	pricing_sync_interval_hours: number;
	model_parameters_url: string;
}

export default function PricingConfigView() {
	const hasSettingsUpdateAccess = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data: bifrostConfig } = useGetCoreConfigQuery({ fromDB: true });
	const config = bifrostConfig?.framework_config;
	const [updateCoreConfig, { isLoading }] = useUpdateCoreConfigMutation();

	const {
		register,
		handleSubmit,
		formState: { errors, isDirty },
		reset,
		watch,
	} = useForm<PricingFormData>({
		defaultValues: {
			pricing_datasheet_url: "",
			pricing_sync_interval_hours: 24,
			model_parameters_url: "",
		},
	});

	const formValues = watch();

	useEffect(() => {
		if (bifrostConfig && config) {
			reset({
				pricing_datasheet_url: config.pricing_url || "",
				pricing_sync_interval_hours: Math.round(config.pricing_sync_interval / 3600) || 24,
				model_parameters_url: config.model_parameters_url || "",
			});
		}
	}, [config, bifrostConfig, reset]);

	const hasChanges = useMemo(() => {
		if (!config || !isDirty) return false;
		const serverUrl = config.pricing_url || "";
		const serverInterval = Math.round(config.pricing_sync_interval / 3600);
		const serverModelParamsUrl = config.model_parameters_url || "";
		return (
			formValues.pricing_datasheet_url !== serverUrl ||
			formValues.pricing_sync_interval_hours !== serverInterval ||
			formValues.model_parameters_url !== serverModelParamsUrl
		);
	}, [config, formValues, isDirty]);

	const onSubmit = async (data: PricingFormData) => {
		try {
			await updateCoreConfig({
				...bifrostConfig!,
				framework_config: {
					...config,
					id: bifrostConfig?.framework_config.id || 0,
					pricing_url: data.pricing_datasheet_url,
					pricing_sync_interval: data.pricing_sync_interval_hours * 3600,
					model_parameters_url: data.model_parameters_url,
				},
			}).unwrap();
			toast.success("价格配置已更新");
			reset(data);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<div className="mx-auto w-full max-w-7xl space-y-4" data-testid="pricing-config-view">
			<form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
				<div>
				<h2 className="text-lg font-semibold tracking-tight">价格配置</h2>
				<p className="text-muted-foreground text-sm">配置自定义价格表和同步间隔。</p>
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
								pattern: {
									value: /^(https?:\/\/)?((localhost|(\d{1,3}\.){3}\d{1,3})(:\d+)?|([\da-z\.-]+)\.([a-z\.]{2,6}))([\/\w \.-]*)*\/?$/,
								message: "请输入有效的 URL。",
								},
								validate: {
									checkIfHttp: (value) => {
										if (!value) return true; // Allow empty
										return value.startsWith("http://") || value.startsWith("https://") || "URL 必须以 http:// 或 https:// 开头";
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
								pattern: {
									value: /^(https?:\/\/)?((localhost|(\d{1,3}\.){3}\d{1,3})(:\d+)?|([\da-z\.-]+)\.([a-z\.]{2,6}))([\/\w \.-]*)*\/?$/,
								message: "请输入有效的 URL。",
								},
								validate: {
									checkIfHttp: (value) => {
										if (!value) return true;
										return value.startsWith("http://") || value.startsWith("https://") || "URL 必须以 http:// 或 https:// 开头";
									},
								},
							})}
							className={errors.model_parameters_url ? "border-destructive" : ""}
						/>
						{errors.model_parameters_url && <p className="text-destructive text-sm">{errors.model_parameters_url.message}</p>}
					</div>

					{/* Pricing Sync Interval */}
					<div className="space-y-2 rounded-sm border p-4">
						<div className="space-y-2">
							<div className="space-y-0.5">
								<Label htmlFor="pricing-sync-interval">价格同步间隔（小时）</Label>
								<p className="text-muted-foreground text-sm">从价格表 URL 同步价格数据的频率。</p>
							</div>
							<Input
								id="pricing-sync-interval"
								type="number"
								className={errors.pricing_sync_interval_hours ? "border-destructive" : ""}
								{...register("pricing_sync_interval_hours", {
									required: "价格同步间隔不能为空",
									min: {
										value: 1,
										message: "同步间隔至少为 1 小时",
									},
									max: {
										value: 8760,
										message: "同步间隔不能超过 8760 小时（一年）",
									},
									valueAsNumber: true,
								})}
							/>
							{errors.pricing_sync_interval_hours && (
								<p className="text-destructive text-sm">{errors.pricing_sync_interval_hours.message}</p>
							)}
						</div>
					</div>
				</div>
				<div className="flex justify-end gap-2 pt-2">
					<Button type="submit" disabled={!hasChanges || isLoading || !hasSettingsUpdateAccess} data-testid="pricing-save-btn">
						{isLoading ? "正在保存..." : "保存修改"}
					</Button>
				</div>
			</form>
		</div>
	);
}
