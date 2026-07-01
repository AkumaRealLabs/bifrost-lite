import {
	useGetLogsProviderCostHistogramQuery,
	useGetLogsProviderLatencyHistogramQuery,
	useGetLogsProviderTTFBHistogramQuery,
	useGetLogsProviderTTFTHistogramQuery,
	useGetLogsProviderTokenHistogramQuery,
	useLazyGetLogsProviderCostHistogramQuery,
	useLazyGetLogsProviderLatencyHistogramQuery,
	useLazyGetLogsProviderTTFBHistogramQuery,
	useLazyGetLogsProviderTTFTHistogramQuery,
	useLazyGetLogsProviderTokenHistogramQuery,
} from "@/lib/store";
import type { LogFilters } from "@/lib/types/logs";
import { forwardRef, useCallback, useImperativeHandle, useMemo } from "react";
import type { DashboardData } from "../../utils/exportUtils";
import type { ChartType } from "../charts/chartTypeToggle";
import { ProviderUsageTab } from "../providerUsageTab";

export interface ProviderUsageTabViewHandle {
	getData: () => Partial<DashboardData>;
	loadData: () => Promise<void>;
}

const sanitizeSeriesLabels = (values?: string[]): string[] => {
	if (!values) return [];
	const trimmed = values.map((v) => v.trim()).filter((v) => v.length > 0);
	return [...new Set(trimmed)];
};

interface ProviderUsageTabViewProps {
	filters: LogFilters;
	active: boolean;
	startTime: number;
	endTime: number;
	providerCostChartType: ChartType;
	providerTokenChartType: ChartType;
	providerLatencyChartType: ChartType;
	providerCostProvider: string;
	providerTokenProvider: string;
	providerLatencyProvider: string;
	onProviderCostChartToggle: (type: ChartType) => void;
	onProviderTokenChartToggle: (type: ChartType) => void;
	onProviderLatencyChartToggle: (type: ChartType) => void;
	onProviderCostProviderChange: (provider: string) => void;
	onProviderTokenProviderChange: (provider: string) => void;
	onProviderLatencyProviderChange: (provider: string) => void;
}

export const ProviderUsageTabView = forwardRef<ProviderUsageTabViewHandle, ProviderUsageTabViewProps>(function ProviderUsageTabView(
	{
		filters,
		active,
		startTime,
		endTime,
		providerCostChartType,
		providerTokenChartType,
		providerLatencyChartType,
		providerCostProvider,
		providerTokenProvider,
		providerLatencyProvider,
		onProviderCostChartToggle,
		onProviderTokenChartToggle,
		onProviderLatencyChartToggle,
		onProviderCostProviderChange,
		onProviderTokenProviderChange,
		onProviderLatencyProviderChange,
	},
	ref,
) {
	const fetchArg = useMemo(() => ({ filters }), [filters]);
	const skipOpts = useMemo(() => ({ skip: !active }), [active]);

	const { data: providerCostData, isLoading: loadingProviderCost } = useGetLogsProviderCostHistogramQuery(fetchArg, skipOpts);
	const { data: providerTokenData, isLoading: loadingProviderTokens } = useGetLogsProviderTokenHistogramQuery(fetchArg, skipOpts);
	const { data: providerLatencyData, isLoading: loadingProviderLatency } = useGetLogsProviderLatencyHistogramQuery(fetchArg, skipOpts);
	const { data: providerTTFBData, isLoading: loadingProviderTTFB } = useGetLogsProviderTTFBHistogramQuery(fetchArg, skipOpts);
	const { data: providerTTFTData, isLoading: loadingProviderTTFT } = useGetLogsProviderTTFTHistogramQuery(fetchArg, skipOpts);

	const [triggerProviderCost] = useLazyGetLogsProviderCostHistogramQuery();
	const [triggerProviderTokens] = useLazyGetLogsProviderTokenHistogramQuery();
	const [triggerProviderLatency] = useLazyGetLogsProviderLatencyHistogramQuery();
	const [triggerProviderTTFB] = useLazyGetLogsProviderTTFBHistogramQuery();
	const [triggerProviderTTFT] = useLazyGetLogsProviderTTFTHistogramQuery();

	const loadData = useCallback(async () => {
		await Promise.all([
			triggerProviderCost(fetchArg, true),
			triggerProviderTokens(fetchArg, true),
			triggerProviderLatency(fetchArg, true),
			triggerProviderTTFB(fetchArg, true),
			triggerProviderTTFT(fetchArg, true),
		]);
	}, [fetchArg, triggerProviderCost, triggerProviderTokens, triggerProviderLatency, triggerProviderTTFB, triggerProviderTTFT]);

	useImperativeHandle(
		ref,
		() => ({
			getData: () => ({
				providerCostData: providerCostData ?? null,
				providerTokenData: providerTokenData ?? null,
				providerLatencyData: providerLatencyData ?? null,
				providerTTFBData: providerTTFBData ?? null,
				providerTTFTData: providerTTFTData ?? null,
			}),
			loadData,
		}),
		[providerCostData, providerTokenData, providerLatencyData, providerTTFBData, providerTTFTData, loadData],
	);

	const availableProviders = useMemo(
		() =>
			sanitizeSeriesLabels([
				...(providerCostData?.providers ?? []),
				...(providerTokenData?.providers ?? []),
				...(providerLatencyData?.providers ?? []),
				...(providerTTFBData?.providers ?? []),
				...(providerTTFTData?.providers ?? []),
			]),
		[
			providerCostData?.providers,
			providerTokenData?.providers,
			providerLatencyData?.providers,
			providerTTFBData?.providers,
			providerTTFTData?.providers,
		],
	);
	const providerCostProviders = useMemo(() => sanitizeSeriesLabels(providerCostData?.providers), [providerCostData?.providers]);
	const providerTokenProviders = useMemo(() => sanitizeSeriesLabels(providerTokenData?.providers), [providerTokenData?.providers]);
	const providerLatencyProviders = useMemo(() => sanitizeSeriesLabels(providerLatencyData?.providers), [providerLatencyData?.providers]);
	const providerTTFBProviders = useMemo(() => sanitizeSeriesLabels(providerTTFBData?.providers), [providerTTFBData?.providers]);
	const providerTTFTProviders = useMemo(() => sanitizeSeriesLabels(providerTTFTData?.providers), [providerTTFTData?.providers]);

	return (
		<ProviderUsageTab
			providerCostData={providerCostData ?? null}
			providerTokenData={providerTokenData ?? null}
			providerLatencyData={providerLatencyData ?? null}
			providerTTFBData={providerTTFBData ?? null}
			providerTTFTData={providerTTFTData ?? null}
			loadingProviderCost={loadingProviderCost}
			loadingProviderTokens={loadingProviderTokens}
			loadingProviderLatency={loadingProviderLatency}
			loadingProviderTTFB={loadingProviderTTFB}
			loadingProviderTTFT={loadingProviderTTFT}
			startTime={startTime}
			endTime={endTime}
			providerCostChartType={providerCostChartType}
			providerTokenChartType={providerTokenChartType}
			providerLatencyChartType={providerLatencyChartType}
			providerCostProvider={providerCostProvider}
			providerTokenProvider={providerTokenProvider}
			providerLatencyProvider={providerLatencyProvider}
			availableProviders={availableProviders}
			providerCostProviders={providerCostProviders}
			providerTokenProviders={providerTokenProviders}
			providerLatencyProviders={providerLatencyProviders}
			providerTTFBProviders={providerTTFBProviders}
			providerTTFTProviders={providerTTFTProviders}
			onProviderCostChartToggle={onProviderCostChartToggle}
			onProviderTokenChartToggle={onProviderTokenChartToggle}
			onProviderLatencyChartToggle={onProviderLatencyChartToggle}
			onProviderCostProviderChange={onProviderCostProviderChange}
			onProviderTokenProviderChange={onProviderTokenProviderChange}
			onProviderLatencyProviderChange={onProviderLatencyProviderChange}
		/>
	);
});
