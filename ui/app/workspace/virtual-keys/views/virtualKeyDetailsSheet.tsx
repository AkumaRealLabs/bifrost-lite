import { SheetNavigationButtons } from "@/components/sheetNavigationButtons";
import { Badge } from "@/components/ui/badge";
import { DottedSeparator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSheetNavigation } from "@/hooks/useSheetNavigation";
import { ProviderIconType, RenderProviderIcon } from "@/lib/constants/icons";
import { ProviderLabels, ProviderName } from "@/lib/constants/logs";
import { isSystemPoolVK, systemPoolLabel } from "@/lib/providerPools";
import { VirtualKey } from "@/lib/types/governance";
import { formatDistanceToNow } from "date-fns";

interface VirtualKeyDetailSheetProps {
	virtualKey: VirtualKey;
	onClose: () => void;
	onNavigate?: (direction: "prev" | "next") => void;
	hasPrev?: boolean;
	hasNext?: boolean;
}

const labelProvider = (provider: string) => ProviderLabels[provider as ProviderName] || provider;

function ModelBadges({ models, emptyLabel }: { models?: string[]; emptyLabel: string }) {
	if (models?.includes("*")) {
		return <Badge variant="success">全部</Badge>;
	}
	if (!models?.length) {
		return <span className="text-muted-foreground text-sm">{emptyLabel}</span>;
	}
	return (
		<div className="flex flex-wrap gap-1">
			{models.map((model) => (
				<Badge key={model} variant="secondary" className="text-xs">
					{model}
				</Badge>
			))}
		</div>
	);
}

export default function VirtualKeyDetailSheet({
	virtualKey,
	onClose,
	onNavigate,
	hasPrev = false,
	hasNext = false,
}: VirtualKeyDetailSheetProps) {
	const { prev: prevKeys, next: nextKeys } = useSheetNavigation({
		enabled: true,
		hasPrev,
		hasNext,
		onNavigate: (direction) => onNavigate?.(direction),
	});
	const isSystemPool = isSystemPoolVK(virtualKey.system_pool || virtualKey.name);
	const poolProviders = virtualKey.pool_providers || [];

	return (
		<Sheet open onOpenChange={onClose}>
			<SheetContent className="flex w-full flex-col overflow-x-hidden p-0 pt-4 sm:max-w-2xl">
				<SheetHeader
					className="flex flex-row items-center justify-between px-0 py-4"
					headerClassName="mb-0 sticky -top-4 bg-card z-10 px-8"
				>
					<div className="flex flex-col items-start">
						<SheetTitle>{virtualKey.name}</SheetTitle>
						<SheetDescription>{virtualKey.description || "虚拟 Key Provider 访问"}</SheetDescription>
					</div>
					<SheetNavigationButtons
						hasPrev={hasPrev}
						hasNext={hasNext}
						onNavigate={(dir) => onNavigate?.(dir)}
						prevKeys={prevKeys}
						nextKeys={nextKeys}
						entityLabel="虚拟 Key"
					/>
				</SheetHeader>

				<div className="space-y-6 overflow-y-auto px-8 py-4">
					<div className="space-y-4">
						<h3 className="font-semibold">基本信息</h3>
						<div className="grid gap-4">
							<div className="grid grid-cols-3 items-center gap-4">
								<span className="text-muted-foreground text-sm">状态</span>
								<div className="col-span-2">
									<Badge variant={virtualKey.is_active ? "default" : "secondary"}>{virtualKey.is_active ? "启用" : "停用"}</Badge>
								</div>
							</div>
							{isSystemPool && (
								<>
									<div className="grid grid-cols-3 items-center gap-4">
										<span className="text-muted-foreground text-sm">系统池</span>
										<div className="col-span-2">
											<Badge variant="success">{systemPoolLabel(virtualKey.system_pool || virtualKey.name)}</Badge>
										</div>
									</div>
									<div className="grid grid-cols-3 items-center gap-4">
										<span className="text-muted-foreground text-sm">池规则</span>
										<div className="col-span-2 font-mono text-sm">{virtualKey.pool_rule}</div>
									</div>
									<div className="grid grid-cols-3 items-center gap-4">
										<span className="text-muted-foreground text-sm">Provider 数</span>
										<div className="col-span-2 text-sm">{virtualKey.provider_count ?? poolProviders.length}</div>
									</div>
								</>
							)}
							<div className="grid grid-cols-3 items-center gap-4">
								<span className="text-muted-foreground text-sm">创建时间</span>
								<div className="col-span-2 text-sm">{formatDistanceToNow(new Date(virtualKey.created_at), { addSuffix: true })}</div>
							</div>
							<div className="grid grid-cols-3 items-center gap-4">
								<span className="text-muted-foreground text-sm">最后更新</span>
								<div className="col-span-2 text-sm">{formatDistanceToNow(new Date(virtualKey.updated_at), { addSuffix: true })}</div>
							</div>
						</div>
					</div>

					<DottedSeparator />

					<div className="space-y-4">
						<h3 className="font-semibold">{isSystemPool ? "自动池 Provider" : "Provider 访问"}</h3>
						{isSystemPool ? (
							poolProviders.length ? (
								<div className="flex flex-wrap gap-2">
									{poolProviders.map((provider) => (
										<Badge key={provider} variant="outline" className="text-xs">
											{provider}
										</Badge>
									))}
								</div>
							) : (
								<p className="text-muted-foreground text-sm">当前没有 Provider 满足系统池规则。</p>
							)
						) : !virtualKey.provider_configs?.length ? (
							<p className="text-muted-foreground text-sm">未配置 Provider，此 Key 将拒绝所有 Provider。</p>
						) : (
							<Table>
								<TableHeader>
									<TableRow>
										<TableHead>Provider</TableHead>
										<TableHead>权重</TableHead>
										<TableHead>允许模型</TableHead>
										<TableHead>禁用模型</TableHead>
										<TableHead>Key</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{virtualKey.provider_configs.map((config, index) => (
										<TableRow key={`${config.provider}-${index}`}>
											<TableCell>
												<div className="flex items-center gap-2">
													<RenderProviderIcon provider={config.provider as ProviderIconType} size="sm" className="h-4 w-4" />
													<span>{labelProvider(config.provider)}</span>
												</div>
											</TableCell>
											<TableCell>{config.weight ?? "-"}</TableCell>
											<TableCell>
												<ModelBadges models={config.allowed_models} emptyLabel="拒绝" />
											</TableCell>
											<TableCell>
												<ModelBadges models={config.blacklisted_models} emptyLabel="无" />
											</TableCell>
											<TableCell>
												{config.allow_all_keys ? (
													<Badge variant="success">全部</Badge>
												) : config.keys?.length ? (
													<div className="flex flex-wrap gap-1">
														{config.keys.map((key) => (
															<Badge key={key.key_id} variant="outline" className="text-xs">
																{key.name}
															</Badge>
														))}
													</div>
												) : (
													<span className="text-muted-foreground text-sm">拒绝</span>
												)}
											</TableCell>
										</TableRow>
									))}
								</TableBody>
							</Table>
						)}
					</div>
				</div>
			</SheetContent>
		</Sheet>
	);
}
