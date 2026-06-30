import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { getErrorMessage, useGetCoreConfigQuery, useUpdateCoreConfigMutation } from "@/lib/store";
import { CoreConfig, DefaultCoreConfig, TTFBRoutingConfig } from "@/lib/types/config";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertTriangle } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

const DEFAULT_TTFB_ROUTING: Required<TTFBRoutingConfig> = {
	enabled: false,
	window_seconds: 900,
	min_samples: 20,
	threshold_ms: 2500,
	min_penalty_factor: 0.2,
};

const normalizeTTFBRouting = (config?: TTFBRoutingConfig): Required<TTFBRoutingConfig> => ({
	enabled: config?.enabled ?? DEFAULT_TTFB_ROUTING.enabled,
	window_seconds: config?.window_seconds ?? DEFAULT_TTFB_ROUTING.window_seconds,
	min_samples: config?.min_samples ?? DEFAULT_TTFB_ROUTING.min_samples,
	threshold_ms: config?.threshold_ms ?? DEFAULT_TTFB_ROUTING.threshold_ms,
	min_penalty_factor: config?.min_penalty_factor ?? DEFAULT_TTFB_ROUTING.min_penalty_factor,
});

const ttfbRoutingEqual = (a?: TTFBRoutingConfig, b?: TTFBRoutingConfig): boolean => {
	const left = normalizeTTFBRouting(a);
	const right = normalizeTTFBRouting(b);
	return (
		left.enabled === right.enabled &&
		left.window_seconds === right.window_seconds &&
		left.min_samples === right.min_samples &&
		left.threshold_ms === right.threshold_ms &&
		left.min_penalty_factor === right.min_penalty_factor
	);
};

export default function PerformanceTuningView() {
	const hasSettingsUpdateAccess = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data: bifrostConfig } = useGetCoreConfigQuery({ fromDB: true });
	const config = bifrostConfig?.client_config;
	const [updateCoreConfig, { isLoading }] = useUpdateCoreConfigMutation();
	const [localConfig, setLocalConfig] = useState<CoreConfig>(DefaultCoreConfig);
	const [needsRestart, setNeedsRestart] = useState<boolean>(false);

	const [localValues, setLocalValues] = useState<{
		initial_pool_size: string;
		max_request_body_size_mb: string;
		ttfb_window_seconds: string;
		ttfb_min_samples: string;
		ttfb_threshold_ms: string;
		ttfb_min_penalty_factor: string;
	}>({
		initial_pool_size: "1000",
		max_request_body_size_mb: "100",
		ttfb_window_seconds: String(DEFAULT_TTFB_ROUTING.window_seconds),
		ttfb_min_samples: String(DEFAULT_TTFB_ROUTING.min_samples),
		ttfb_threshold_ms: String(DEFAULT_TTFB_ROUTING.threshold_ms),
		ttfb_min_penalty_factor: String(DEFAULT_TTFB_ROUTING.min_penalty_factor),
	});

	useEffect(() => {
		if (bifrostConfig && config) {
			const ttfbRouting = normalizeTTFBRouting(config.ttfb_routing);
			setLocalConfig({ ...config, ttfb_routing: ttfbRouting });
			setLocalValues({
				initial_pool_size: config?.initial_pool_size?.toString() || "1000",
				max_request_body_size_mb: config?.max_request_body_size_mb?.toString() || "100",
				ttfb_window_seconds: String(ttfbRouting.window_seconds),
				ttfb_min_samples: String(ttfbRouting.min_samples),
				ttfb_threshold_ms: String(ttfbRouting.threshold_ms),
				ttfb_min_penalty_factor: String(ttfbRouting.min_penalty_factor),
			});
			setNeedsRestart(false);
		}
	}, [config, bifrostConfig]);

	const hasChanges = useMemo(() => {
		if (!config) return false;
		return (
			localConfig.initial_pool_size !== config.initial_pool_size ||
			localConfig.max_request_body_size_mb !== config.max_request_body_size_mb ||
			!ttfbRoutingEqual(localConfig.ttfb_routing, config.ttfb_routing)
		);
	}, [config, localConfig]);

	const handlePoolSizeChange = useCallback((value: string) => {
		setLocalValues((prev) => ({ ...prev, initial_pool_size: value }));
		const numValue = Number.parseInt(value);
		if (!isNaN(numValue) && numValue > 0) {
			setLocalConfig((prev) => ({ ...prev, initial_pool_size: numValue }));
		}
		setNeedsRestart(true);
	}, []);

	const handleMaxRequestBodySizeMBChange = useCallback((value: string) => {
		setLocalValues((prev) => ({ ...prev, max_request_body_size_mb: value }));
		const numValue = Number.parseInt(value);
		if (!isNaN(numValue) && numValue > 0) {
			setLocalConfig((prev) => ({ ...prev, max_request_body_size_mb: numValue }));
		}
		setNeedsRestart(true);
	}, []);

	const handleTTFBRoutingToggle = useCallback((checked: boolean) => {
		setLocalConfig((prev) => ({
			...prev,
			ttfb_routing: {
				...normalizeTTFBRouting(prev.ttfb_routing),
				enabled: checked,
			},
		}));
	}, []);

	const handleTTFBRoutingNumberChange = useCallback((field: keyof Omit<TTFBRoutingConfig, "enabled">, value: string) => {
		const localValueKey = `ttfb_${field}` as keyof typeof localValues;
		setLocalValues((prev) => ({ ...prev, [localValueKey]: value }));

		const numValue = field === "window_seconds" || field === "min_samples" ? Number.parseInt(value) : Number.parseFloat(value);
		if (Number.isFinite(numValue) && numValue > 0 && (field !== "min_penalty_factor" || numValue <= 1)) {
			setLocalConfig((prev) => ({
				...prev,
				ttfb_routing: {
					...normalizeTTFBRouting(prev.ttfb_routing),
					[field]: numValue,
				},
			}));
		}
	}, []);

	const handleSave = useCallback(async () => {
		try {
			const poolSize = Number.parseInt(localValues.initial_pool_size);
			const maxBodySize = Number.parseInt(localValues.max_request_body_size_mb);
			const ttfbWindowSeconds = Number.parseInt(localValues.ttfb_window_seconds);
			const ttfbMinSamples = Number.parseInt(localValues.ttfb_min_samples);
			const ttfbThresholdMs = Number.parseFloat(localValues.ttfb_threshold_ms);
			const ttfbMinPenaltyFactor = Number.parseFloat(localValues.ttfb_min_penalty_factor);

			if (isNaN(poolSize) || poolSize <= 0) {
				toast.error("初始池大小必须是正数。");
				return;
			}

			if (isNaN(maxBodySize) || maxBodySize <= 0) {
				toast.error("最大请求体大小必须是正数。");
				return;
			}

			if (isNaN(ttfbWindowSeconds) || ttfbWindowSeconds <= 0) {
				toast.error("TTFB 统计窗口必须是正整数。");
				return;
			}

			if (isNaN(ttfbMinSamples) || ttfbMinSamples <= 0) {
				toast.error("TTFB 最小样本数必须是正整数。");
				return;
			}

			if (isNaN(ttfbThresholdMs) || ttfbThresholdMs <= 0) {
				toast.error("TTFB P95 阈值必须是正数。");
				return;
			}

			if (isNaN(ttfbMinPenaltyFactor) || ttfbMinPenaltyFactor <= 0 || ttfbMinPenaltyFactor > 1) {
				toast.error("最低保留权重必须大于 0 且不超过 1。");
				return;
			}

			if (!bifrostConfig) {
				toast.error("配置尚未加载，请刷新后重试。");
				return;
			}
			const nextConfig: CoreConfig = {
				...localConfig,
				initial_pool_size: poolSize,
				max_request_body_size_mb: maxBodySize,
				ttfb_routing: {
					enabled: localConfig.ttfb_routing?.enabled ?? false,
					window_seconds: ttfbWindowSeconds,
					min_samples: ttfbMinSamples,
					threshold_ms: ttfbThresholdMs,
					min_penalty_factor: ttfbMinPenaltyFactor,
				},
			};
			await updateCoreConfig({ ...bifrostConfig, client_config: nextConfig }).unwrap();
			setNeedsRestart(false);
			toast.success("性能设置已更新");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}, [bifrostConfig, localConfig, localValues, updateCoreConfig]);

	return (
		<div className="mx-auto w-full max-w-4xl space-y-4">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">性能调优</h2>
				<p className="text-muted-foreground text-sm">配置性能相关设置。</p>
			</div>

			<Alert variant="warning">
				<AlertTriangle className="h-4 w-4" />
				<AlertDescription>
					初始池大小和最大请求体大小需要重启 Bifrost 后才会完全生效。TTFB 路由调度保存后会对下一次 VK 加权路由生效。
				</AlertDescription>
			</Alert>

			<div className="space-y-4">
				{/* Initial Pool Size */}
				<div>
					<div className="flex items-center justify-between space-x-2 rounded-sm border p-4">
						<div className="space-y-0.5">
							<label htmlFor="initial-pool-size" className="text-sm font-medium">
								初始池大小
							</label>
							<p className="text-muted-foreground text-sm">初始连接池大小。</p>
						</div>
						<Input
							id="initial-pool-size"
							type="number"
							className="w-24"
							value={localValues.initial_pool_size}
							onChange={(e) => handlePoolSizeChange(e.target.value)}
							min="1"
							disabled={!hasSettingsUpdateAccess}
						/>
					</div>
					{needsRestart && <RestartWarning />}
				</div>

				{/* Max Request Body Size */}
				<div>
					<div className="flex items-center justify-between space-x-2 rounded-sm border p-4">
						<div className="space-y-0.5">
							<label htmlFor="max-request-body-size-mb" className="text-sm font-medium">
								最大请求体大小（MB）
							</label>
							<p className="text-muted-foreground text-sm">请求体的最大大小，单位为 MB。</p>
						</div>
						<Input
							id="max-request-body-size-mb"
							type="number"
							className="w-24"
							value={localValues.max_request_body_size_mb}
							onChange={(e) => handleMaxRequestBodySizeMBChange(e.target.value)}
							min="1"
							disabled={!hasSettingsUpdateAccess}
						/>
					</div>
					{needsRestart && <RestartWarning />}
				</div>

				<div className="space-y-4 rounded-sm border p-4">
					<div className="flex items-center justify-between gap-4">
						<div className="space-y-0.5">
							<label htmlFor="ttfb-routing-enabled" className="text-sm font-medium">
								TTFB 路由调度
							</label>
							<p className="text-muted-foreground text-sm">
								开启后按最近流式请求的 provider+model P95 TTFB 对慢 provider 降权；不会自动放大快 provider 权重。
							</p>
						</div>
						<Switch
							id="ttfb-routing-enabled"
							checked={localConfig.ttfb_routing?.enabled ?? false}
							onCheckedChange={handleTTFBRoutingToggle}
							disabled={!hasSettingsUpdateAccess}
						/>
					</div>

					<div className="grid gap-4 md:grid-cols-2">
						<div className="space-y-2">
							<label htmlFor="ttfb-window-seconds" className="text-sm font-medium">
								统计窗口（秒）
							</label>
							<Input
								id="ttfb-window-seconds"
								type="number"
								min="1"
								step="1"
								value={localValues.ttfb_window_seconds}
								onChange={(e) => handleTTFBRoutingNumberChange("window_seconds", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
							<p className="text-muted-foreground text-xs">默认 900 秒，即最近 15 分钟。</p>
						</div>

						<div className="space-y-2">
							<label htmlFor="ttfb-min-samples" className="text-sm font-medium">
								最小样本数
							</label>
							<Input
								id="ttfb-min-samples"
								type="number"
								min="1"
								step="1"
								value={localValues.ttfb_min_samples}
								onChange={(e) => handleTTFBRoutingNumberChange("min_samples", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
							<p className="text-muted-foreground text-xs">样本不足时使用原始权重，不惩罚。</p>
						</div>

						<div className="space-y-2">
							<label htmlFor="ttfb-threshold-ms" className="text-sm font-medium">
								P95 阈值（ms）
							</label>
							<Input
								id="ttfb-threshold-ms"
								type="number"
								min="1"
								step="100"
								value={localValues.ttfb_threshold_ms}
								onChange={(e) => handleTTFBRoutingNumberChange("threshold_ms", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
							<p className="text-muted-foreground text-xs">低于阈值保持原权重；高于阈值按比例降权。</p>
						</div>

						<div className="space-y-2">
							<label htmlFor="ttfb-min-penalty-factor" className="text-sm font-medium">
								最低保留权重
							</label>
							<Input
								id="ttfb-min-penalty-factor"
								type="number"
								min="0.01"
								max="1"
								step="0.05"
								value={localValues.ttfb_min_penalty_factor}
								onChange={(e) => handleTTFBRoutingNumberChange("min_penalty_factor", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
							<p className="text-muted-foreground text-xs">默认 0.2，表示最慢也保留 20% 原始权重。</p>
						</div>
					</div>
				</div>
			</div>
			<div className="flex justify-end pt-2">
				<Button onClick={handleSave} disabled={!hasChanges || isLoading || !hasSettingsUpdateAccess}>
					{isLoading ? "正在保存..." : "保存修改"}
				</Button>
			</div>
		</div>
	);
}

const RestartWarning = () => {
	return <div className="text-muted-foreground mt-2 pl-4 text-xs font-semibold">需要重启 Bifrost 才能应用修改。</div>;
};