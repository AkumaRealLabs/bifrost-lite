import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { getErrorMessage, useGetCoreConfigQuery, useUpdateCoreConfigMutation } from "@/lib/store";
import { CoreConfig, DefaultCoreConfig, ProviderScoringConfig } from "@/lib/types/config";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertTriangle } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

type NormalizedProviderScoring = Required<
	Omit<ProviderScoringConfig, "weights"> & { weights: Required<NonNullable<ProviderScoringConfig["weights"]>> }
>;

type LocalValues = {
	initial_pool_size: string;
	max_request_body_size_mb: string;
	window_seconds: string;
	min_samples: string;
	error_rate_threshold: string;
	consecutive_failures_threshold: string;
	cooldown_seconds: string;
	ttft_threshold_ms: string;
	weight_availability: string;
	weight_ttft: string;
	weight_cost: string;
};

const DEFAULT_PROVIDER_SCORING: NormalizedProviderScoring = {
	enabled: false,
	window_seconds: 120,
	min_samples: 5,
	error_rate_threshold: 0.3,
	consecutive_failures_threshold: 3,
	cooldown_seconds: 300,
	ttft_threshold_ms: 2500,
	weights: { availability: 0.7, ttft: 0.2, cost: 0.1 },
};

const normalizeProviderScoring = (config?: ProviderScoringConfig): NormalizedProviderScoring => ({
	...DEFAULT_PROVIDER_SCORING,
	...config,
	weights: { ...DEFAULT_PROVIDER_SCORING.weights, ...config?.weights },
});

const providerScoringEqual = (a?: ProviderScoringConfig, b?: ProviderScoringConfig) => {
	const left = normalizeProviderScoring(a);
	const right = normalizeProviderScoring(b);
	return (
		left.enabled === right.enabled &&
		left.window_seconds === right.window_seconds &&
		left.min_samples === right.min_samples &&
		left.error_rate_threshold === right.error_rate_threshold &&
		left.consecutive_failures_threshold === right.consecutive_failures_threshold &&
		left.cooldown_seconds === right.cooldown_seconds &&
		left.ttft_threshold_ms === right.ttft_threshold_ms &&
		left.weights.availability === right.weights.availability &&
		left.weights.ttft === right.weights.ttft &&
		left.weights.cost === right.weights.cost
	);
};

const toLocalValues = (config: CoreConfig, scoring: NormalizedProviderScoring): LocalValues => ({
	initial_pool_size: config.initial_pool_size?.toString() || "1000",
	max_request_body_size_mb: config.max_request_body_size_mb?.toString() || "100",
	window_seconds: String(scoring.window_seconds),
	min_samples: String(scoring.min_samples),
	error_rate_threshold: String(scoring.error_rate_threshold),
	consecutive_failures_threshold: String(scoring.consecutive_failures_threshold),
	cooldown_seconds: String(scoring.cooldown_seconds),
	ttft_threshold_ms: String(scoring.ttft_threshold_ms),
	weight_availability: String(scoring.weights.availability),
	weight_ttft: String(scoring.weights.ttft),
	weight_cost: String(scoring.weights.cost),
});

export default function PerformanceTuningView() {
	const hasSettingsUpdateAccess = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data: bifrostConfig } = useGetCoreConfigQuery({ fromDB: true });
	const config = bifrostConfig?.client_config;
	const [updateCoreConfig, { isLoading }] = useUpdateCoreConfigMutation();
	const [localConfig, setLocalConfig] = useState<CoreConfig>(DefaultCoreConfig);
	const [needsRestart, setNeedsRestart] = useState<boolean>(false);
	const [localValues, setLocalValues] = useState<LocalValues>(toLocalValues(DefaultCoreConfig, DEFAULT_PROVIDER_SCORING));

	useEffect(() => {
		if (bifrostConfig && config) {
			const providerScoring = normalizeProviderScoring(config.provider_scoring);
			setLocalConfig({ ...config, provider_scoring: providerScoring });
			setLocalValues(toLocalValues(config, providerScoring));
			setNeedsRestart(false);
		}
	}, [config, bifrostConfig]);

	const hasChanges = useMemo(() => {
		if (!config) return false;
		return (
			localConfig.initial_pool_size !== config.initial_pool_size ||
			localConfig.max_request_body_size_mb !== config.max_request_body_size_mb ||
			!providerScoringEqual(localConfig.provider_scoring, config.provider_scoring)
		);
	}, [config, localConfig]);

	const handleProviderScoringToggle = useCallback((checked: boolean) => {
		setLocalConfig((prev) => ({
			...prev,
			provider_scoring: { ...normalizeProviderScoring(prev.provider_scoring), enabled: checked },
		}));
	}, []);

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

	const handleProviderScoringNumberChange = useCallback(
		(field: keyof Omit<NormalizedProviderScoring, "enabled" | "weights">, value: string) => {
			setLocalValues((prev) => ({ ...prev, [field]: value }));
			const integerFields = new Set(["window_seconds", "min_samples", "consecutive_failures_threshold", "cooldown_seconds"]);
			const numValue = integerFields.has(field) ? Number.parseInt(value) : Number.parseFloat(value);
			if (
				Number.isFinite(numValue) &&
				((field === "error_rate_threshold" && numValue >= 0 && numValue <= 1) || (field !== "error_rate_threshold" && numValue > 0))
			) {
				setLocalConfig((prev) => ({
					...prev,
					provider_scoring: { ...normalizeProviderScoring(prev.provider_scoring), [field]: numValue },
				}));
			}
		},
		[],
	);

	const handleProviderScoringWeightChange = useCallback((field: keyof NormalizedProviderScoring["weights"], value: string) => {
		setLocalValues((prev) => ({ ...prev, [`weight_${field}`]: value }));
		const numValue = Number.parseFloat(value);
		if (Number.isFinite(numValue) && ((field === "availability" && numValue > 0) || (field !== "availability" && numValue >= 0))) {
			setLocalConfig((prev) => ({
				...prev,
				provider_scoring: {
					...normalizeProviderScoring(prev.provider_scoring),
					weights: { ...normalizeProviderScoring(prev.provider_scoring).weights, [field]: numValue },
				},
			}));
		}
	}, []);

	const handleSave = useCallback(async () => {
		try {
			const poolSize = Number.parseInt(localValues.initial_pool_size);
			const maxBodySize = Number.parseInt(localValues.max_request_body_size_mb);
			const scoring: NormalizedProviderScoring = {
				enabled: localConfig.provider_scoring?.enabled ?? false,
				window_seconds: Number.parseInt(localValues.window_seconds),
				min_samples: Number.parseInt(localValues.min_samples),
				error_rate_threshold: Number.parseFloat(localValues.error_rate_threshold),
				consecutive_failures_threshold: Number.parseInt(localValues.consecutive_failures_threshold),
				cooldown_seconds: Number.parseInt(localValues.cooldown_seconds),
				ttft_threshold_ms: Number.parseFloat(localValues.ttft_threshold_ms),
				weights: {
					availability: Number.parseFloat(localValues.weight_availability),
					ttft: Number.parseFloat(localValues.weight_ttft),
					cost: Number.parseFloat(localValues.weight_cost),
				},
			};

			if (isNaN(poolSize) || poolSize <= 0) {
				toast.error("初始池大小必须是正数。");
				return;
			}
			if (isNaN(maxBodySize) || maxBodySize <= 0) {
				toast.error("最大请求体大小必须是正数。");
				return;
			}
			if (isNaN(scoring.window_seconds) || scoring.window_seconds <= 0) {
				toast.error("统计窗口必须是正整数。");
				return;
			}
			if (isNaN(scoring.min_samples) || scoring.min_samples <= 0) {
				toast.error("最小样本数必须是正整数。");
				return;
			}
			if (isNaN(scoring.error_rate_threshold) || scoring.error_rate_threshold < 0 || scoring.error_rate_threshold > 1) {
				toast.error("错误率停调阈值必须在 0 到 1 之间。");
				return;
			}
			if (isNaN(scoring.consecutive_failures_threshold) || scoring.consecutive_failures_threshold <= 0) {
				toast.error("连续失败阈值必须是正整数。");
				return;
			}
			if (isNaN(scoring.cooldown_seconds) || scoring.cooldown_seconds <= 0) {
				toast.error("停调时间必须是正整数。");
				return;
			}
			if (isNaN(scoring.ttft_threshold_ms) || scoring.ttft_threshold_ms <= 0) {
				toast.error("TTFT P95 阈值必须是正数。");
				return;
			}
			const weightSum = scoring.weights.availability + scoring.weights.ttft + scoring.weights.cost;
			if (
				isNaN(scoring.weights.availability) ||
				isNaN(scoring.weights.ttft) ||
				isNaN(scoring.weights.cost) ||
				scoring.weights.availability <= 0 ||
				scoring.weights.ttft < 0 ||
				scoring.weights.cost < 0 ||
				weightSum <= 0
			) {
				toast.error("权重配置无效：可用性必须大于 0，TTFT 和成本不能小于 0。");
				return;
			}
			if (!bifrostConfig || !config) {
				toast.error("配置尚未加载，请刷新后重试。");
				return;
			}

			const nextConfig: CoreConfig = {
				...config,
				initial_pool_size: poolSize,
				max_request_body_size_mb: maxBodySize,
				provider_scoring: scoring,
			};
			await updateCoreConfig({ ...bifrostConfig, client_config: nextConfig }).unwrap();
			setNeedsRestart(false);
			toast.success("性能设置已更新");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}, [bifrostConfig, config, localConfig.provider_scoring?.enabled, localValues, updateCoreConfig]);

	return (
		<div className="mx-auto w-full max-w-4xl space-y-4">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">性能调优</h2>
				<p className="text-muted-foreground text-sm">配置性能相关设置。</p>
			</div>

			<Alert variant="warning">
				<AlertTriangle className="h-4 w-4" />
				<AlertDescription>
					初始池大小和最大请求体大小需要重启 Bifrost 后才会完全生效。智能路由评分保存后会对下一次 VK 自动路由生效。
				</AlertDescription>
			</Alert>

			<div className="space-y-4">
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
							<label htmlFor="provider-scoring-enabled" className="text-sm font-medium">
								智能路由评分
							</label>
							<p className="text-muted-foreground text-sm">优先级：高可用 &gt; TTFT &gt; 成本。仅影响 VK 自动路由。</p>
							<p className="text-muted-foreground text-xs">
								评分过程、临时停调和最终选择在请求日志 Routing tab 查看。成本分来自 Provider 的渠道成本（RMB / 1刀额度），不是模型价格覆盖。
							</p>
						</div>
						<Switch
							id="provider-scoring-enabled"
							checked={localConfig.provider_scoring?.enabled ?? false}
							onCheckedChange={handleProviderScoringToggle}
							disabled={!hasSettingsUpdateAccess}
						/>
					</div>

					<div className="grid gap-4 md:grid-cols-3">
						<div className="space-y-2">
							<label htmlFor="provider-scoring-window-seconds" className="text-sm font-medium">
								统计窗口（秒）
							</label>
							<Input
								id="provider-scoring-window-seconds"
								type="number"
								min="1"
								step="1"
								value={localValues.window_seconds}
								onChange={(e) => handleProviderScoringNumberChange("window_seconds", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-min-samples" className="text-sm font-medium">
								最小样本数
							</label>
							<Input
								id="provider-scoring-min-samples"
								type="number"
								min="1"
								step="1"
								value={localValues.min_samples}
								onChange={(e) => handleProviderScoringNumberChange("min_samples", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-ttft-threshold-ms" className="text-sm font-medium">
								TTFT P95 阈值（ms）
							</label>
							<Input
								id="provider-scoring-ttft-threshold-ms"
								type="number"
								min="1"
								step="100"
								value={localValues.ttft_threshold_ms}
								onChange={(e) => handleProviderScoringNumberChange("ttft_threshold_ms", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-error-rate-threshold" className="text-sm font-medium">
								错误率停调阈值
							</label>
							<Input
								id="provider-scoring-error-rate-threshold"
								type="number"
								min="0"
								max="1"
								step="0.05"
								value={localValues.error_rate_threshold}
								onChange={(e) => handleProviderScoringNumberChange("error_rate_threshold", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-consecutive-failures-threshold" className="text-sm font-medium">
								连续失败阈值
							</label>
							<Input
								id="provider-scoring-consecutive-failures-threshold"
								type="number"
								min="1"
								step="1"
								value={localValues.consecutive_failures_threshold}
								onChange={(e) => handleProviderScoringNumberChange("consecutive_failures_threshold", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-cooldown-seconds" className="text-sm font-medium">
								停调时间（秒）
							</label>
							<Input
								id="provider-scoring-cooldown-seconds"
								type="number"
								min="1"
								step="1"
								value={localValues.cooldown_seconds}
								onChange={(e) => handleProviderScoringNumberChange("cooldown_seconds", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-weight-availability" className="text-sm font-medium">
								可用性权重
							</label>
							<Input
								id="provider-scoring-weight-availability"
								type="number"
								min="0.01"
								step="0.05"
								value={localValues.weight_availability}
								onChange={(e) => handleProviderScoringWeightChange("availability", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-weight-ttft" className="text-sm font-medium">
								TTFT 权重
							</label>
							<Input
								id="provider-scoring-weight-ttft"
								type="number"
								min="0"
								step="0.05"
								value={localValues.weight_ttft}
								onChange={(e) => handleProviderScoringWeightChange("ttft", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
						</div>

						<div className="space-y-2">
							<label htmlFor="provider-scoring-weight-cost" className="text-sm font-medium">
								成本权重
							</label>
							<Input
								id="provider-scoring-weight-cost"
								type="number"
								min="0"
								step="0.05"
								value={localValues.weight_cost}
								onChange={(e) => handleProviderScoringWeightChange("cost", e.target.value)}
								disabled={!hasSettingsUpdateAccess}
							/>
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
