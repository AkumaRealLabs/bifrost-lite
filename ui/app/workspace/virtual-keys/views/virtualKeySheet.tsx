import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DottedSeparator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ProviderIconType, RenderProviderIcon } from "@/lib/constants/icons";
import { ProviderLabels, ProviderName } from "@/lib/constants/logs";
import {
	getErrorMessage,
	useCreateVirtualKeyMutation,
	useGetAllKeysQuery,
	useGetProvidersQuery,
	useRotateVirtualKeyMutation,
	useUpdateVirtualKeyMutation,
} from "@/lib/store";
import { KnownProvider, ModelProvider } from "@/lib/types/config";
import { CreateVirtualKeyRequest, DBKey, UpdateVirtualKeyRequest, VirtualKey } from "@/lib/types/governance";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { RotateCcw, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

interface VirtualKeySheetProps {
	virtualKey?: VirtualKey | null;
	onSave: () => void;
	onCancel: () => void;
}

type LiteProviderConfig = {
	id?: number;
	provider: string;
	weight: string;
	allowedModelsText: string;
	blockedModelsText: string;
	keyIds: string[];
};

const listToText = (items?: string[]) => (items?.length ? items.join(", ") : "");

const parseList = (value: string) =>
	value
		.split(/[,\n]+/)
		.map((item) => item.trim())
		.filter(Boolean);

const providerLabel = (provider: string) => ProviderLabels[provider as ProviderName] || provider;

function toProviderConfigs(virtualKey?: VirtualKey | null): LiteProviderConfig[] {
	return (
		virtualKey?.provider_configs?.map((config) => ({
			id: config.id,
			provider: config.provider,
			weight: config.weight == null ? "" : String(config.weight),
			allowedModelsText: listToText(config.allowed_models),
			blockedModelsText: listToText(config.blacklisted_models),
			keyIds: config.allow_all_keys ? ["*"] : config.keys?.map((key) => key.key_id) || [],
		})) || []
	);
}

function normalizeProviderConfigs(configs: LiteProviderConfig[], clearGovernance: boolean) {
	return configs.map((config) => {
		const weight = config.weight.trim() === "" ? null : Number(config.weight);
		return {
			id: config.id,
			provider: config.provider,
			weight: Number.isFinite(weight) ? weight : null,
			allowed_models: parseList(config.allowedModelsText),
			blacklisted_models: parseList(config.blockedModelsText),
			key_ids: config.keyIds,
			...(clearGovernance ? { budgets: [], rate_limit: {} } : {}),
		};
	});
}

function ProviderKeySelector({
	config,
	keys,
	onChange,
}: {
	config: LiteProviderConfig;
	keys: DBKey[];
	onChange: (keyIds: string[]) => void;
}) {
	const allowAll = config.keyIds.includes("*");

	return (
		<div className="space-y-2">
			<Label className="text-sm font-medium">允许的 Key</Label>
			<label className="flex items-center gap-2 text-sm">
				<Checkbox checked={allowAll} onCheckedChange={(checked) => onChange(checked ? ["*"] : [])} />
				<span>允许当前和未来所有 Key</span>
			</label>
			{!allowAll && (
				<div className="max-h-40 space-y-2 overflow-y-auto rounded-sm border p-2">
					{keys.length === 0 ? (
						<p className="text-muted-foreground text-sm">这个 Provider 还没有配置 Key。</p>
					) : (
						keys.map((key) => (
							<label key={key.key_id} className="flex items-center justify-between gap-3 text-sm">
								<span className="truncate">{key.name}</span>
								<Checkbox
									checked={config.keyIds.includes(key.key_id)}
									onCheckedChange={(checked) => {
										const next = checked
											? [...config.keyIds, key.key_id]
											: config.keyIds.filter((keyID) => keyID !== key.key_id);
										onChange(next);
									}}
								/>
							</label>
						))
					)}
				</div>
			)}
		</div>
	);
}

export default function VirtualKeySheet({ virtualKey, onSave, onCancel }: VirtualKeySheetProps) {
	const [isOpen, setIsOpen] = useState(true);
	const [name, setName] = useState(virtualKey?.name || "");
	const [description, setDescription] = useState(virtualKey?.description || "");
	const [isActive, setIsActive] = useState(virtualKey?.is_active ?? true);
	const [selectedProvider, setSelectedProvider] = useState("");
	const [providerConfigs, setProviderConfigs] = useState<LiteProviderConfig[]>(() => toProviderConfigs(virtualKey));
	const [showRotateWarning, setShowRotateWarning] = useState(false);
	const isEditing = !!virtualKey;

	const hasCreateAccess = useRbac(RbacResource.VirtualKeys, RbacOperation.Create);
	const hasUpdateAccess = useRbac(RbacResource.VirtualKeys, RbacOperation.Update);
	const canSubmit = isEditing ? hasUpdateAccess : hasCreateAccess;

	const { data: providersData = [], error: providersError } = useGetProvidersQuery();
	const { data: keysData = [], error: keysError } = useGetAllKeysQuery();
	const [createVirtualKey, { isLoading: isCreating }] = useCreateVirtualKeyMutation();
	const [updateVirtualKey, { isLoading: isUpdating }] = useUpdateVirtualKeyMutation();
	const [rotateVirtualKey, { isLoading: isRotating }] = useRotateVirtualKeyMutation();
	const isLoading = isCreating || isUpdating || isRotating;

	const providers = providersData as ModelProvider[];
	const keysByProvider = useMemo(() => {
		const grouped = new Map<string, DBKey[]>();
		for (const key of keysData) {
			const list = grouped.get(key.provider) || [];
			list.push(key);
			grouped.set(key.provider, list);
		}
		return grouped;
	}, [keysData]);

	if (providersError) toast.error(`加载 Provider 失败：${getErrorMessage(providersError)}`);
	if (keysError) toast.error(`加载 Provider Key 失败：${getErrorMessage(keysError)}`);

	const handleClose = () => {
		setIsOpen(false);
		setTimeout(onCancel, 150);
	};

	const updateProviderConfig = (index: number, patch: Partial<LiteProviderConfig>) => {
		setProviderConfigs((current) => current.map((config, i) => (i === index ? { ...config, ...patch } : config)));
	};

	const addProvider = (provider: string) => {
		if (!provider || providerConfigs.some((config) => config.provider === provider)) return;
		setProviderConfigs((current) => [
			...current,
			{
				provider,
				weight: "",
				allowedModelsText: "*",
				blockedModelsText: "",
				keyIds: ["*"],
			},
		]);
	};

	const submit = async () => {
		const trimmedName = name.trim();
		if (!trimmedName) {
			toast.error("虚拟 Key 名称必填");
			return;
		}
		if (!canSubmit) {
			toast.error("你没有权限执行此操作");
			return;
		}

		try {
			if (isEditing && virtualKey) {
				const data: UpdateVirtualKeyRequest = {
					name: trimmedName,
					description,
					is_active: isActive,
					provider_configs: normalizeProviderConfigs(providerConfigs, true),
					budgets: [],
					rate_limit: {},
					calendar_aligned: false,
				};
				await updateVirtualKey({ vkId: virtualKey.id, data }).unwrap();
				toast.success("虚拟 Key 已更新");
			} else {
				const data: CreateVirtualKeyRequest = {
					name: trimmedName,
					description: description || undefined,
					is_active: isActive,
					provider_configs: normalizeProviderConfigs(providerConfigs, false),
				};
				await createVirtualKey(data).unwrap();
				toast.success("虚拟 Key 已创建");
			}
			onSave();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const rotate = async () => {
		if (!virtualKey || !hasUpdateAccess) return;
		try {
			await rotateVirtualKey(virtualKey.id).unwrap();
			toast.success("虚拟 Key 已轮换");
			setShowRotateWarning(false);
			onSave();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<Sheet open={isOpen} onOpenChange={(open) => !open && handleClose()}>
			<SheetContent
				className="flex w-full flex-col gap-4 overflow-x-hidden p-0 pt-4"
				data-testid="vk-sheet-content"
				onInteractOutside={(e) => e.preventDefault()}
				onEscapeKeyDown={handleClose}
			>
				<SheetHeader className="flex flex-col items-start px-0 py-4" headerClassName="mb-0 sticky -top-4 bg-card z-10 px-8">
					<SheetTitle>{isEditing ? virtualKey?.name : "创建虚拟 Key"}</SheetTitle>
					<SheetDescription>
						{isEditing ? "更新网关访问和 Provider 路由。" : "创建用于网关访问的虚拟 Key。"}
					</SheetDescription>
				</SheetHeader>

				<div className="grow space-y-6 overflow-y-auto px-8">
					<div className="space-y-4">
						<div className="space-y-2">
							<Label htmlFor="vk-name-input">名称 *</Label>
							<Input id="vk-name-input" value={name} onChange={(event) => setName(event.target.value)} data-testid="vk-name-input" />
						</div>
						<div className="space-y-2">
							<Label htmlFor="vk-description-input">描述</Label>
							<Textarea
								id="vk-description-input"
								value={description}
								onChange={(event) => setDescription(event.target.value)}
								rows={3}
								data-testid="vk-description-input"
							/>
						</div>
						<div className="flex items-center justify-between rounded-sm border px-3 py-2">
							<div>
								<Label htmlFor="vk-is-active-toggle">启用</Label>
								<p className="text-muted-foreground text-xs">停用后，此虚拟 Key 将不能再发起请求。</p>
							</div>
							<Switch id="vk-is-active-toggle" checked={isActive} onCheckedChange={setIsActive} data-testid="vk-is-active-toggle" />
						</div>
					</div>

					<DottedSeparator />

					<div className="space-y-4">
						<div className="flex items-center justify-between gap-3">
							<div>
								<Label className="text-sm font-medium">Provider 访问</Label>
								<p className="text-muted-foreground text-xs">权重范围 0 到 1；留空表示不参加自动加权负载均衡。</p>
							</div>
							<Select
								value={selectedProvider}
								onValueChange={(provider) => {
									addProvider(provider);
									setSelectedProvider("");
								}}
							>
								<SelectTrigger className="w-[240px]" data-testid="vk-provider-select">
									<SelectValue placeholder="添加 Provider" />
								</SelectTrigger>
								<SelectContent>
									{providers
										.filter((provider) => provider.name && !providerConfigs.some((config) => config.provider === provider.name))
										.map((provider) => (
											<SelectItem key={provider.name} value={provider.name}>
												<RenderProviderIcon
													provider={
														provider.custom_provider_config?.base_provider_type || (provider.name as KnownProvider)
													}
													size="sm"
													className="h-4 w-4"
												/>
												{provider.custom_provider_config ? provider.name : providerLabel(provider.name)}
											</SelectItem>
										))}
								</SelectContent>
							</Select>
						</div>

						{providerConfigs.length === 0 ? (
							<p className="text-muted-foreground rounded-sm border p-3 text-sm">未配置 Provider，此 Key 将拒绝所有 Provider。</p>
						) : (
							<div className="space-y-3">
								{providerConfigs.map((config, index) => {
									const provider = providers.find((item) => item.name === config.provider);
									const providerKeys = keysByProvider.get(config.provider) || [];
									return (
										<div key={`${config.provider}-${index}`} className="space-y-4 rounded-sm border p-4">
											<div className="flex items-center justify-between gap-3">
												<div className="flex min-w-0 items-center gap-2">
													<RenderProviderIcon
														provider={
															provider?.custom_provider_config?.base_provider_type || (config.provider as ProviderIconType)
														}
														size="sm"
														className="h-4 w-4"
													/>
													<span className="truncate font-medium">{provider?.custom_provider_config ? config.provider : providerLabel(config.provider)}</span>
												</div>
												<Button
													type="button"
													variant="ghost"
													size="icon"
													aria-label={`移除 ${config.provider}`}
													data-testid={`vk-delete-provider-${index}`}
													onClick={() => setProviderConfigs((current) => current.filter((_, i) => i !== index))}
												>
													<Trash2 className="h-4 w-4" />
												</Button>
											</div>

											<div className="grid gap-4 md:grid-cols-[140px_1fr]">
												<div className="space-y-2">
													<Label htmlFor={`vk-weight-${index}`}>权重</Label>
													<Input
														id={`vk-weight-${index}`}
														type="number"
														min="0"
														max="1"
														step="0.01"
														placeholder="不参加自动均衡"
														value={config.weight}
														onChange={(event) => updateProviderConfig(index, { weight: event.target.value })}
														data-testid={`vk-weight-input-${index}`}
													/>
												</div>
												<div className="space-y-2">
													<Label htmlFor={`vk-allowed-models-${index}`}>允许模型</Label>
													<Input
														id={`vk-allowed-models-${index}`}
														placeholder="*，或用逗号分隔模型名"
														value={config.allowedModelsText}
														onChange={(event) => updateProviderConfig(index, { allowedModelsText: event.target.value })}
														data-testid={`vk-models-input-${index}`}
													/>
												</div>
											</div>

											<div className="space-y-2">
												<Label htmlFor={`vk-blocked-models-${index}`}>禁用模型</Label>
												<Input
													id={`vk-blocked-models-${index}`}
													placeholder="用逗号分隔模型名"
													value={config.blockedModelsText}
													onChange={(event) => updateProviderConfig(index, { blockedModelsText: event.target.value })}
													data-testid={`vk-blocked-models-input-${index}`}
												/>
											</div>

											<ProviderKeySelector
												config={config}
												keys={providerKeys}
												onChange={(keyIds) => updateProviderConfig(index, { keyIds })}
											/>
										</div>
									);
								})}
							</div>
						)}
					</div>
				</div>

				{showRotateWarning && (
					<div className="mx-8 rounded-sm border border-destructive/40 p-3">
						<div className="mb-3 text-sm">
							确定轮换 <span className="font-medium">{virtualKey?.name}</span> 的密钥值？Key ID 和 Provider 访问保持不变。
						</div>
						<div className="flex justify-end gap-2">
							<Button type="button" variant="outline" onClick={() => setShowRotateWarning(false)} data-testid="vk-rotate-cancel-btn">
								取消
							</Button>
							<Button type="button" variant="destructive" onClick={rotate} disabled={isRotating} data-testid="vk-rotate-confirm-btn">
								{isRotating ? "正在轮换..." : "轮换 Key"}
							</Button>
						</div>
					</div>
				)}

				{isEditing && virtualKey?.config_hash && (
					<div className="px-8">
						<Badge variant="outline">来自配置同步</Badge>
					</div>
				)}

				<div className="border-border bg-card sticky bottom-0 z-10 border-t px-8 py-4">
					<div className="flex items-center justify-between gap-2">
						{isEditing ? (
							<Button
								type="button"
								variant="outline"
								onClick={() => setShowRotateWarning(true)}
								disabled={!hasUpdateAccess || isRotating}
								data-testid="vk-rotate-btn"
							>
								<RotateCcw className="h-4 w-4" />
								{isRotating ? "正在轮换..." : "轮换 Key"}
							</Button>
						) : (
							<span />
						)}
						<div className="flex justify-end gap-2">
							<Button type="button" variant="outline" onClick={handleClose} data-testid="vk-cancel-btn">
								取消
							</Button>
							<Button type="button" disabled={isLoading || !canSubmit} onClick={submit} data-testid="vk-save-btn">
								{isLoading ? "正在保存..." : isEditing ? "更新" : "创建"}
							</Button>
						</div>
					</div>
				</div>
			</SheetContent>
		</Sheet>
	);
}
