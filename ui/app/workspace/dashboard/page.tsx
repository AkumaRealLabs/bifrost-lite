import { LogsFilterSidebar } from "@/components/filters/logsFilterSidebar";
import { DateTimePickerWithRange } from "@/components/ui/datePickerWithRange";
import { ScrollArea } from "@/components/ui/scrollArea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTimezonePreference } from "@/lib/hooks/useTimezonePreference";
import type { LogFilters } from "@/lib/types/logs";
import { dateUtils } from "@/lib/types/logs";
import { getRangeForPeriod, TIME_PERIODS } from "@/lib/utils/timeRange";
import { useLocation } from "@tanstack/react-router";
import { parseAsInteger, parseAsString, useQueryStates } from "nuqs";
import { useCallback, useMemo, useRef, useState } from "react";
import { type ChartType } from "./components/charts/chartTypeToggle";
import { ExportPopover } from "./components/exportPopover";
import { type DimensionRankingsTabViewHandle, DimensionRankingsTabView } from "./components/tabViews/dimensionRankingsTabView";
import { type ModelRankingsTabViewHandle, ModelRankingsTabView } from "./components/tabViews/modelRankingsTabView";
import { type OverviewTabViewHandle, OverviewTabView } from "./components/tabViews/overviewTabView";
import { type ProviderUsageTabViewHandle, ProviderUsageTabView } from "./components/tabViews/providerUsageTabView";
import type { DashboardData } from "./utils/exportUtils";

const toChartType = (value: string): ChartType => (value === "line" ? "line" : "bar");

const parseCsvParam = (value: string): string[] => (value ? value.split(",").filter(Boolean) : []);

export default function DashboardPage() {
	const defaultTimeRange = useMemo(() => dateUtils.getDefaultTimeRange(), []);

	const [timezone, setTimezone] = useTimezonePreference();

	const { search } = useLocation();
	const hasExplicitTimeRange = (search as Record<string, unknown>)?.start_time && (search as Record<string, unknown>)?.end_time;

	const [urlState, setUrlState] = useQueryStates(
		{
			period: parseAsString.withDefault(hasExplicitTimeRange ? "" : "1h").withOptions({ clearOnDefault: false }),
			start_time: parseAsInteger.withDefault(defaultTimeRange.startTime),
			end_time: parseAsInteger.withDefault(defaultTimeRange.endTime),
			tab: parseAsString.withDefault("overview"),
			virtual_key_ids: parseAsString.withDefault(""),
			providers: parseAsString.withDefault(""),
			models: parseAsString.withDefault(""),
			selected_key_ids: parseAsString.withDefault(""),
			objects: parseAsString.withDefault(""),
			status: parseAsString.withDefault(""),
			stop_reasons: parseAsString.withDefault(""),
			missing_cost_only: parseAsString.withDefault("false"),
			metadata_filters: parseAsString.withDefault(""),
			volume_chart: parseAsString.withDefault("bar"),
			token_chart: parseAsString.withDefault("bar"),
			cost_chart: parseAsString.withDefault("bar"),
			model_chart: parseAsString.withDefault("bar"),
			latency_chart: parseAsString.withDefault("bar"),
			cost_model: parseAsString.withDefault("all"),
			usage_model: parseAsString.withDefault("all"),
			provider_cost_chart: parseAsString.withDefault("bar"),
			provider_token_chart: parseAsString.withDefault("bar"),
			provider_latency_chart: parseAsString.withDefault("bar"),
			provider_cost_provider: parseAsString.withDefault("all"),
			provider_token_provider: parseAsString.withDefault("all"),
			provider_latency_provider: parseAsString.withDefault("all"),
			parent_request_id: parseAsString.withDefault(""),
			aliases: parseAsString.withDefault(""),
		},
		{
			history: "push",
			shallow: false,
		},
	);

	// Parse filter arrays from URL state
	const selectedProviders = useMemo(() => parseCsvParam(urlState.providers), [urlState.providers]);
	const selectedModels = useMemo(() => parseCsvParam(urlState.models), [urlState.models]);
	const selectedKeyIds = useMemo(() => parseCsvParam(urlState.selected_key_ids), [urlState.selected_key_ids]);
	const selectedVirtualKeyIds = useMemo(() => parseCsvParam(urlState.virtual_key_ids), [urlState.virtual_key_ids]);
	const selectedTypes = useMemo(() => parseCsvParam(urlState.objects), [urlState.objects]);
	const selectedStatuses = useMemo(() => parseCsvParam(urlState.status), [urlState.status]);
	const selectedStopReasons = useMemo(() => parseCsvParam(urlState.stop_reasons), [urlState.stop_reasons]);
	const missingCostOnly = useMemo(() => urlState.missing_cost_only === "true", [urlState.missing_cost_only]);
	const metadataFilters = useMemo(() => {
		if (!urlState.metadata_filters) return undefined;
		try {
			return JSON.parse(urlState.metadata_filters) as Record<string, string>;
		} catch {
			return undefined;
		}
	}, [urlState.metadata_filters]);

	const selectedAliases = useMemo(() => parseCsvParam(urlState.aliases), [urlState.aliases]);

	const filters: LogFilters = useMemo(
		() => ({
			...(urlState.period
				? { period: urlState.period }
				: {
						start_time: dateUtils.toISOString(urlState.start_time),
						end_time: dateUtils.toISOString(urlState.end_time),
					}),
			...(selectedProviders.length > 0 && { providers: selectedProviders }),
			...(selectedModels.length > 0 && { models: selectedModels }),
			...(selectedKeyIds.length > 0 && { selected_key_ids: selectedKeyIds }),
			...(selectedVirtualKeyIds.length > 0 && {
				virtual_key_ids: selectedVirtualKeyIds,
			}),
			...(selectedTypes.length > 0 && { objects: selectedTypes }),
			...(selectedStatuses.length > 0 && { status: selectedStatuses }),
			...(selectedStopReasons.length > 0 && { stop_reasons: selectedStopReasons }),
			...(missingCostOnly && { missing_cost_only: true }),
			...(metadataFilters &&
				Object.keys(metadataFilters).length > 0 && {
					metadata_filters: metadataFilters,
				}),
			...(urlState.parent_request_id && { parent_request_id: urlState.parent_request_id }),
			...(selectedAliases.length > 0 && { aliases: selectedAliases }),
		}),
		[
			urlState.period,
			urlState.start_time,
			urlState.end_time,
			urlState.parent_request_id,
			selectedProviders,
			selectedModels,
			selectedKeyIds,
			selectedVirtualKeyIds,
			selectedTypes,
			selectedStatuses,
			selectedStopReasons,
			missingCostOnly,
			metadataFilters,
			selectedAliases,
		],
	);

	// Tab view refs for export data aggregation
	const overviewRef = useRef<OverviewTabViewHandle>(null);
	const providerRef = useRef<ProviderUsageTabViewHandle>(null);
	const modelRankingsRef = useRef<ModelRankingsTabViewHandle>(null);
	const virtualKeyRankingsRef = useRef<DimensionRankingsTabViewHandle>(null);

	const allRefs = [overviewRef, providerRef, modelRankingsRef, virtualKeyRankingsRef];

	const getDashboardData = useCallback((): DashboardData => {
		const merged: Partial<DashboardData> = {};
		for (const r of allRefs) {
			if (r.current) Object.assign(merged, r.current.getData());
		}
		return {
			histogramData: null,
			tokenData: null,
			costData: null,
			modelData: null,
			latencyData: null,
			ttfbData: null,
			ttftData: null,
			providerCostData: null,
			providerTokenData: null,
			providerLatencyData: null,
			providerTTFBData: null,
			providerTTFTData: null,
			rankingsData: null,
			virtualKeyRankingsData: null,
			...merged,
		};
	}, []);

	const handlePreloadData = useCallback(async () => {
		await Promise.all(allRefs.map((r) => r.current?.loadData()));
	}, []);

	// Tab change handler
	const handleTabChange = useCallback(
		(tab: string) => {
			setUrlState({ tab });
		},
		[setUrlState],
	);

	// Chart type toggles
	const handleVolumeChartToggle = useCallback((type: ChartType) => setUrlState({ volume_chart: type }), [setUrlState]);
	const handleTokenChartToggle = useCallback((type: ChartType) => setUrlState({ token_chart: type }), [setUrlState]);
	const handleCostChartToggle = useCallback((type: ChartType) => setUrlState({ cost_chart: type }), [setUrlState]);
	const handleModelChartToggle = useCallback((type: ChartType) => setUrlState({ model_chart: type }), [setUrlState]);
	const handleLatencyChartToggle = useCallback((type: ChartType) => setUrlState({ latency_chart: type }), [setUrlState]);
	const handleProviderCostChartToggle = useCallback((type: ChartType) => setUrlState({ provider_cost_chart: type }), [setUrlState]);
	const handleProviderTokenChartToggle = useCallback((type: ChartType) => setUrlState({ provider_token_chart: type }), [setUrlState]);
	const handleProviderLatencyChartToggle = useCallback((type: ChartType) => setUrlState({ provider_latency_chart: type }), [setUrlState]);

	// Model / provider filter changes
	const handleCostModelChange = useCallback((model: string) => setUrlState({ cost_model: model }), [setUrlState]);
	const handleUsageModelChange = useCallback((model: string) => setUrlState({ usage_model: model }), [setUrlState]);
	const handleProviderCostProviderChange = useCallback(
		(provider: string) => setUrlState({ provider_cost_provider: provider }),
		[setUrlState],
	);
	const handleProviderTokenProviderChange = useCallback(
		(provider: string) => setUrlState({ provider_token_provider: provider }),
		[setUrlState],
	);
	const handleProviderLatencyProviderChange = useCallback(
		(provider: string) => setUrlState({ provider_latency_provider: provider }),
		[setUrlState],
	);

	// Adapter: converts a full LogFilters object to dashboard's CSV-based URL state
	const setFilters = useCallback(
		(newFilters: LogFilters) => {
			const newStartTime = newFilters.start_time ? dateUtils.toUnixTimestamp(new Date(newFilters.start_time)) : undefined;
			const newEndTime = newFilters.end_time ? dateUtils.toUnixTimestamp(new Date(newFilters.end_time)) : undefined;
			const timeChanged = newStartTime !== urlState.start_time || newEndTime !== urlState.end_time;
			setUrlState({
				...(timeChanged && { period: "" }),
				start_time: newStartTime,
				end_time: newEndTime,
				period: urlState.period,
				providers: (newFilters.providers || []).join(","),
				models: (newFilters.models || []).join(","),
				selected_key_ids: (newFilters.selected_key_ids || []).join(","),
				virtual_key_ids: (newFilters.virtual_key_ids || []).join(","),
				objects: (newFilters.objects || []).join(","),
				status: (newFilters.status || []).join(","),
				stop_reasons: (newFilters.stop_reasons || []).join(","),
				missing_cost_only: String(newFilters.missing_cost_only ?? false),
				metadata_filters:
					newFilters.metadata_filters && Object.keys(newFilters.metadata_filters).length > 0
						? JSON.stringify(newFilters.metadata_filters)
						: "",
				parent_request_id: newFilters.parent_request_id || "",
				aliases: (newFilters.aliases || []).join(","),
			});
		},
		[setUrlState, urlState.start_time, urlState.end_time, urlState.period],
	);

	// Date range for picker
	const dateRange = useMemo(
		() => ({
			from: dateUtils.fromUnixTimestamp(urlState.start_time),
			to: dateUtils.fromUnixTimestamp(urlState.end_time),
		}),
		[urlState.start_time, urlState.end_time],
	);

	const handlePeriodChange = useCallback(
		(period: string | undefined) => {
			if (!period) return;
			const { from, to } = getRangeForPeriod(period);
			setUrlState({
				period,
				start_time: Math.floor(from.getTime() / 1000),
				end_time: Math.floor(to.getTime() / 1000),
			});
		},
		[setUrlState],
	);

	const handleDateRangeChange = useCallback(
		(range: { from?: Date; to?: Date }) => {
			if (!range.from || !range.to) return;
			setUrlState({
				period: "",
				start_time: dateUtils.toUnixTimestamp(range.from),
				end_time: dateUtils.toUnixTimestamp(range.to),
			});
		},
		[setUrlState],
	);

	// PDF export mode
	const [pdfMode, setPdfMode] = useState(false);
	const dashboardMinHeightRef = useRef<string>("");
	const hiddenTabsRef = useRef<HTMLElement[]>([]);

	const handlePdfExport = useCallback(async (): Promise<HTMLElement[]> => {
		await handlePreloadData();
		setPdfMode(true);

		await new Promise<void>((resolve) => {
			requestAnimationFrame(() => {
				requestAnimationFrame(() => resolve());
			});
		});

		const hiddenTabs = document.querySelectorAll<HTMLElement>('[data-slot="tabs-content"][hidden]');
		hiddenTabsRef.current = Array.from(hiddenTabs);
		for (const tab of hiddenTabs) {
			tab.removeAttribute("hidden");
			tab.style.display = "block";
		}

		const dashboardEl = document.getElementById("dashboard-root");
		if (dashboardEl) {
			dashboardMinHeightRef.current = dashboardEl.style.minHeight;
			dashboardEl.style.minHeight = "0";
		}

		window.dispatchEvent(new Event("resize"));
		await new Promise<void>((resolve) => {
			requestAnimationFrame(() => {
				requestAnimationFrame(() => resolve());
			});
		});

		const ids = [
			"dashboard-section-overview",
			"dashboard-section-provider-usage",
			"dashboard-section-rankings",
			"dashboard-section-virtual-key-rankings",
		];
		return ids.map((id) => document.getElementById(id)).filter(Boolean) as HTMLElement[];
	}, [handlePreloadData]);

	const handlePdfExportDone = useCallback(() => {
		const dashboardEl = document.getElementById("dashboard-root");
		if (dashboardEl) {
			dashboardEl.style.minHeight = dashboardMinHeightRef.current;
		}

		for (const tab of hiddenTabsRef.current) {
			tab.setAttribute("hidden", "");
			tab.style.display = "";
		}
		hiddenTabsRef.current = [];

		setPdfMode(false);
	}, []);

	const activeTab = urlState.tab || "overview";

	return (
		<div id="dashboard-root" className="no-padding-parent no-border-parent bg-background flex h-[calc(100vh_-_16px)] w-full gap-3">
			{/* Sidebar Filters */}
			<LogsFilterSidebar filters={filters} onFiltersChange={setFilters} />

			{/* Main Content */}
			<ScrollArea className="bg-card flex min-w-0 flex-1 flex-col gap-4 rounded-l-md" viewportClassName="no-table">
				{/* Header */}
				<div className="flex items-center justify-between p-4">
					<div className="flex items-center gap-2">
						<h1 className="text-lg font-semibold">看板</h1>
					</div>
					<div className="flex items-center gap-2">
						<ExportPopover
							getData={getDashboardData}
							onPreloadData={handlePreloadData}
							onPdfExport={handlePdfExport}
							onPdfExportDone={handlePdfExportDone}
						/>
						<DateTimePickerWithRange
							dateTime={dateRange}
							onDateTimeUpdate={handleDateRangeChange}
							preDefinedPeriods={TIME_PERIODS}
							predefinedPeriod={urlState.period || undefined}
							onPredefinedPeriodChange={handlePeriodChange}
							triggerTestId="dashboard-filter-daterange"
							popupAlignment="end"
							showTimezone
							timezone={timezone}
							onTimezoneChange={setTimezone}
						/>
					</div>
				</div>

				<div className="p-4">
					{/* Tabs */}
					<Tabs value={activeTab} onValueChange={handleTabChange}>
						<div className="mb-2 max-w-full overflow-x-auto">
							<TabsList className="w-max min-w-max">
								<TabsTrigger className="shrink-0" value="overview" data-testid="dashboard-tab-overview">
									总览
								</TabsTrigger>
								<TabsTrigger className="shrink-0" value="provider-usage" data-testid="dashboard-tab-provider-usage">
									Provider 用量
								</TabsTrigger>
								<TabsTrigger className="shrink-0" value="rankings" data-testid="dashboard-tab-rankings">
									模型排行
								</TabsTrigger>
								<TabsTrigger className="shrink-0" value="virtual-key-rankings" data-testid="dashboard-tab-virtual-key-rankings">
									虚拟 Key 排行
								</TabsTrigger>
							</TabsList>
						</div>

						{/* Overview Tab */}
						<TabsContent value="overview" {...(pdfMode && { forceMount: true })}>
							<div id="dashboard-section-overview">
								<OverviewTabView
									ref={overviewRef}
									filters={filters}
									active={activeTab === "overview" || pdfMode}
									startTime={urlState.start_time}
									endTime={urlState.end_time}
									volumeChartType={toChartType(urlState.volume_chart)}
									tokenChartType={toChartType(urlState.token_chart)}
									costChartType={toChartType(urlState.cost_chart)}
									modelChartType={toChartType(urlState.model_chart)}
									latencyChartType={toChartType(urlState.latency_chart)}
									costModel={urlState.cost_model}
									usageModel={urlState.usage_model}
									onVolumeChartToggle={handleVolumeChartToggle}
									onTokenChartToggle={handleTokenChartToggle}
									onCostChartToggle={handleCostChartToggle}
									onModelChartToggle={handleModelChartToggle}
									onLatencyChartToggle={handleLatencyChartToggle}
									onCostModelChange={handleCostModelChange}
									onUsageModelChange={handleUsageModelChange}
								/>
							</div>
						</TabsContent>

						{/* Provider Usage Tab */}
						<TabsContent value="provider-usage" {...(pdfMode && { forceMount: true })}>
							<div id="dashboard-section-provider-usage">
								<ProviderUsageTabView
									ref={providerRef}
									filters={filters}
									active={activeTab === "provider-usage" || pdfMode}
									startTime={urlState.start_time}
									endTime={urlState.end_time}
									providerCostChartType={toChartType(urlState.provider_cost_chart)}
									providerTokenChartType={toChartType(urlState.provider_token_chart)}
									providerLatencyChartType={toChartType(urlState.provider_latency_chart)}
									providerCostProvider={urlState.provider_cost_provider}
									providerTokenProvider={urlState.provider_token_provider}
									providerLatencyProvider={urlState.provider_latency_provider}
									onProviderCostChartToggle={handleProviderCostChartToggle}
									onProviderTokenChartToggle={handleProviderTokenChartToggle}
									onProviderLatencyChartToggle={handleProviderLatencyChartToggle}
									onProviderCostProviderChange={handleProviderCostProviderChange}
									onProviderTokenProviderChange={handleProviderTokenProviderChange}
									onProviderLatencyProviderChange={handleProviderLatencyProviderChange}
								/>
							</div>
						</TabsContent>

						{/* Model Rankings Tab */}
						<TabsContent value="rankings" {...(pdfMode && { forceMount: true })}>
							<div id="dashboard-section-rankings">
								<ModelRankingsTabView
									ref={modelRankingsRef}
									filters={filters}
									active={activeTab === "rankings" || pdfMode}
									startTime={urlState.start_time}
									endTime={urlState.end_time}
								/>
							</div>
						</TabsContent>

						{/* Virtual Key Rankings Tab */}
						<TabsContent value="virtual-key-rankings" {...(pdfMode && { forceMount: true })}>
							<div id="dashboard-section-virtual-key-rankings">
								<DimensionRankingsTabView
									ref={virtualKeyRankingsRef}
									filters={filters}
									active={activeTab === "virtual-key-rankings" || pdfMode}
									dimension="virtual_key"
									dimensionLabel="虚拟 Key"
									testIdPrefix="dashboard-virtual-key-rankings"
									dataKey="virtualKeyRankingsData"
								/>
							</div>
						</TabsContent>
					</Tabs>
				</div>
			</ScrollArea>
		</div>
	);
}
