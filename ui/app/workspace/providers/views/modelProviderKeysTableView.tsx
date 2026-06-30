import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import { Button } from "@/components/ui/button";
import { CardHeader, CardTitle } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdownMenu";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getErrorMessage } from "@/lib/store";
import { useDeleteProviderKeyMutation, useGetProviderKeysQuery, useUpdateProviderKeyMutation } from "@/lib/store/apis/providersApi";
import { ModelProvider } from "@/lib/types/config";
import { cn } from "@/lib/utils";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertCircle, CheckCircle2, EllipsisIcon, PencilIcon, PlusIcon, TrashIcon } from "lucide-react";
import { ReactNode, useState } from "react";
import { toast } from "sonner";
import AddNewKeySheet from "../dialogs/addNewKeySheet";

interface Props {
	className?: string;
	provider: ModelProvider;
	headerActions?: ReactNode;
	isKeyless?: boolean;
}

function ProviderKeyActionsMenu({
	keyId,
	hasUpdateAccess,
	hasDeleteAccess,
	onEdit,
	onDelete,
}: {
	keyId: string;
	hasUpdateAccess: boolean;
	hasDeleteAccess: boolean;
	onEdit: (keyId: string) => void;
	onDelete: (keyId: string) => void;
}) {
	const [isOpen, setIsOpen] = useState(false);

	return (
		<DropdownMenu open={isOpen} onOpenChange={setIsOpen}>
			<DropdownMenuTrigger asChild>
				<Button onClick={(e) => e.stopPropagation()} variant="ghost">
					<EllipsisIcon className="h-5 w-5" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end">
				<DropdownMenuItem
					onSelect={(e) => {
						e.preventDefault();
						onEdit(keyId);
						setIsOpen(false);
					}}
					disabled={!hasUpdateAccess}
				>
					<PencilIcon className="mr-1 h-4 w-4" />
					编辑
				</DropdownMenuItem>
				<DropdownMenuItem
					variant="destructive"
					onSelect={(e) => {
						e.preventDefault();
						onDelete(keyId);
						setIsOpen(false);
					}}
					disabled={!hasDeleteAccess}
				>
					<TrashIcon className="mr-1 h-4 w-4" />
					删除
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export default function ModelProviderKeysTableView({ provider, className, headerActions, isKeyless }: Props) {
	const providerName = provider.name?.toLowerCase() ?? "";
	const isVLLM = providerName === "vllm";
	const isOllamaOrSGL = providerName === "ollama" || providerName === "sgl";
	const entityLabel = isVLLM ? "model" : isOllamaOrSGL ? "server" : "key";
	const entityLabelZh = isVLLM ? "模型" : isOllamaOrSGL ? "服务" : "Key";
	const entityLabelPluralZh = isVLLM ? "模型" : isOllamaOrSGL ? "服务" : "Key";
	const hasUpdateProviderAccess = useRbac(RbacResource.ModelProvider, RbacOperation.Update);
	const hasDeleteProviderAccess = useRbac(RbacResource.ModelProvider, RbacOperation.Delete);
	const [updateProviderKey, { isLoading: isUpdatingProviderKey }] = useUpdateProviderKeyMutation();
	const [deleteProviderKey, { isLoading: isDeletingProviderKey }] = useDeleteProviderKeyMutation();
	const { data: keys = [] } = useGetProviderKeysQuery(provider.name);
	const isMutatingProviderKey = isUpdatingProviderKey || isDeletingProviderKey;
	const [togglingKeyIds, setTogglingKeyIds] = useState<Set<string>>(new Set());
	const [showAddNewKeyDialog, setShowAddNewKeyDialog] = useState<{ show: boolean; keyId: string | null } | undefined>(undefined);
	const [showDeleteKeyDialog, setShowDeleteKeyDialog] = useState<{ show: boolean; keyId: string } | undefined>(undefined);

	function handleAddKey() {
		setShowAddNewKeyDialog({ show: true, keyId: null });
	}

	return (
		<div className={cn("w-full", className)}>
			{showDeleteKeyDialog && (
				<AlertDialog open={showDeleteKeyDialog.show}>
					<AlertDialogContent onClick={(e) => e.stopPropagation()}>
						<AlertDialogHeader>
							<AlertDialogTitle>删除{entityLabelZh}</AlertDialogTitle>
							<AlertDialogDescription>
								确认删除这个{entityLabelZh}吗？此操作不可撤销。
							</AlertDialogDescription>
						</AlertDialogHeader>
						<AlertDialogFooter className="pt-4">
							<AlertDialogCancel onClick={() => setShowDeleteKeyDialog(undefined)} disabled={isMutatingProviderKey}>
								取消
							</AlertDialogCancel>
							<AlertDialogAction
								disabled={isMutatingProviderKey || !hasDeleteProviderAccess}
								onClick={() => {
									deleteProviderKey({
										provider: provider.name,
										keyId: showDeleteKeyDialog.keyId,
									})
										.unwrap()
										.then(() => {
												toast.success(`${entityLabelZh}已删除`);
											setShowDeleteKeyDialog(undefined);
										})
										.catch((err) => {
												toast.error(`删除${entityLabelZh}失败`, {
												description: getErrorMessage(err),
											});
										});
								}}
							>
								删除
							</AlertDialogAction>
						</AlertDialogFooter>
					</AlertDialogContent>
				</AlertDialog>
			)}
			{showAddNewKeyDialog && (
				<AddNewKeySheet
					show={showAddNewKeyDialog.show}
					onCancel={() => setShowAddNewKeyDialog(undefined)}
					provider={provider}
					keyId={showAddNewKeyDialog.keyId}
					providerName={providerName}
				/>
			)}
			<CardHeader className="mb-4 px-0">
				<CardTitle className="flex items-center justify-between">
						<div className="flex items-center gap-2">已配置{entityLabelPluralZh}</div>
					<div className="flex items-center gap-2">
						{headerActions}
						{!isKeyless && hasUpdateProviderAccess ? (
							<Button
								disabled={!hasUpdateProviderAccess}
								data-testid="add-key-btn"
								onClick={() => {
									handleAddKey();
								}}
							>
								<PlusIcon className="h-4 w-4" />
								新增{entityLabelZh}
							</Button>
						) : null}
					</div>
				</CardTitle>
			</CardHeader>
			{isKeyless ? (
				<div className="text-muted-foreground flex flex-col items-center justify-center gap-2 rounded-sm border py-10 text-center text-sm">
					<p>这是无 Key Provider，不需要配置 API Key。</p>
					<p>可使用上方按钮编辑 Provider 配置。</p>
				</div>
			) : (
				<div className="flex w-full flex-col gap-2 rounded-sm border">
					<Table className="w-full table-fixed" data-testid="keys-table">
						<colgroup>
							<col className="w-[64%]" />
							<col className="w-[12%]" />
							<col className="w-[12%]" />
							<col className="w-[12%]" />
						</colgroup>
						<TableHeader className="w-full">
							<TableRow>
								<TableHead>{isVLLM ? "模型" : isOllamaOrSGL ? "服务" : "API Key"}</TableHead>
								<TableHead>权重</TableHead>
								<TableHead>启用</TableHead>
								<TableHead className="text-right"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{keys.length === 0 && (
								<TableRow data-testid="keys-table-empty-state">
									<TableCell colSpan={4} className="py-6 text-center">
											没有找到{entityLabelPluralZh}。
									</TableCell>
								</TableRow>
							)}
							{keys.map((key) => {
								const isKeyEnabled = key.enabled ?? true;
								return (
									<TableRow
										key={key.id}
										data-testid={`key-row-${key.name}`}
										className="text-sm transition-colors hover:bg-white"
										onClick={() => {}}
									>
										<TableCell className="overflow-hidden">
											<div className="flex min-w-0 items-center space-x-2">
												{key.status === "success" && (
													<Tooltip>
														<TooltipTrigger asChild>
															<button
																type="button"
																	aria-label="Key 状态：模型列表可用"
																data-testid={`key-status-success-${key.name}`}
																className="inline-flex"
															>
																<CheckCircle2 aria-hidden className="h-4 w-4 flex-shrink-0 text-green-600" />
															</button>
														</TooltipTrigger>
															<TooltipContent>模型列表可用</TooltipContent>
													</Tooltip>
												)}
												{key.status === "list_models_failed" &&
													(() => {
														// Check if the failure might be due to an env var that the server couldn't resolve
														const hasSecretVarConfig =
															(key.azure_key_config?.endpoint?.type && key.azure_key_config.endpoint.type !== "plain_text") ||
															(key.vertex_key_config?.project_id?.type && key.vertex_key_config.project_id.type !== "plain_text") ||
															(key.vertex_key_config?.region?.type && key.vertex_key_config.region.type !== "plain_text") ||
															(key.bedrock_key_config?.region?.type && key.bedrock_key_config.region.type !== "plain_text") ||
															(key.vllm_key_config?.url?.type && key.vllm_key_config.url.type !== "plain_text") ||
															(key.value?.type && key.value.type !== "plain_text");
														const isEnvResolutionError =
															hasSecretVarConfig && key.description && /not set|empty|missing/i.test(key.description);

														return isEnvResolutionError ? (
															<Tooltip>
																<TooltipTrigger asChild>
																	<button
																		type="button"
																			aria-label="Key 状态：密钥引用可能未解析"
																		data-testid={`key-status-warning-${key.name}`}
																		className="inline-flex"
																	>
																		<AlertCircle aria-hidden className="h-4 w-4 flex-shrink-0 text-orange-500" />
																	</button>
																</TooltipTrigger>
																<TooltipContent className="max-w-xs break-words">
																		{key.description} - 请确认服务端已配置该密钥引用
																</TooltipContent>
															</Tooltip>
														) : (
															<Tooltip>
																<TooltipTrigger asChild>
																	<button
																		type="button"
																			aria-label="Key 状态：模型列表失败"
																		data-testid={`key-status-error-${key.name}`}
																		className="inline-flex"
																	>
																		<AlertCircle aria-hidden className="text-destructive h-4 w-4 flex-shrink-0" />
																	</button>
																</TooltipTrigger>
																<TooltipContent className="max-w-xs break-words">
																		{key.description || "此 Key 的模型发现失败"}
																</TooltipContent>
															</Tooltip>
														);
													})()}
												<span className="truncate font-mono text-sm">{key.name}</span>
											</div>
										</TableCell>
										<TableCell data-testid="key-weight-value">
											<div className="flex items-center space-x-2">
												<span className="font-mono text-sm">{key.weight}</span>
											</div>
										</TableCell>
										<TableCell>
											<Switch
												data-testid="key-enabled-switch"
												checked={isKeyEnabled}
												size="md"
												disabled={!hasUpdateProviderAccess || togglingKeyIds.has(key.id)}
												onAsyncCheckedChange={async (checked) => {
													setTogglingKeyIds((prev) => new Set(prev).add(key.id));
													await updateProviderKey({
														provider: provider.name,
														keyId: key.id,
														key: { ...key, enabled: checked },
													})
														.unwrap()
														.then(() => {
																toast.success(`${entityLabelZh}已${checked ? "启用" : "停用"}`);
														})
														.catch((err) => {
																toast.error(`更新${entityLabelZh}失败`, { description: getErrorMessage(err) });
														})
														.finally(() => {
															setTogglingKeyIds((prev) => {
																const next = new Set(prev);
																next.delete(key.id);
																return next;
															});
														});
												}}
											/>
										</TableCell>
										<TableCell className="text-right">
											<div className="flex items-center justify-end space-x-2">
												{hasUpdateProviderAccess || hasDeleteProviderAccess ? (
													<ProviderKeyActionsMenu
														keyId={key.id}
														hasUpdateAccess={hasUpdateProviderAccess}
														hasDeleteAccess={hasDeleteProviderAccess}
														onEdit={(keyId) => setShowAddNewKeyDialog({ show: true, keyId })}
														onDelete={(keyId) => setShowDeleteKeyDialog({ show: true, keyId })}
													/>
												) : null}
											</div>
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}
