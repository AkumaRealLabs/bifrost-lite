import { Button } from "@/components/ui/button";
import { KeyRound } from "lucide-react";

interface VirtualKeysEmptyStateProps {
	onAddClick: () => void;
	canCreate?: boolean;
}

export function VirtualKeysEmptyState({ onAddClick, canCreate = true }: VirtualKeysEmptyStateProps) {
	return (
		<div
			className="flex min-h-[80vh] w-full flex-col items-center justify-center gap-4 py-16 text-center"
			data-testid="virtual-keys-empty-state"
		>
			<div className="text-muted-foreground">
				<KeyRound className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />
			</div>
			<div className="flex flex-col gap-1">
				<h1 className="text-muted-foreground text-xl font-medium">虚拟 Key 用于控制网关访问</h1>
				<div className="text-muted-foreground mx-auto mt-2 max-w-[600px] text-sm font-normal">
					为客户端创建虚拟 Key，并通过 Provider 权限控制路由访问。
				</div>
				<div className="mx-auto mt-6 flex flex-row flex-wrap items-center justify-center gap-2">
					<Button aria-label="添加第一个虚拟 Key" onClick={onAddClick} disabled={!canCreate} data-testid="create-vk-btn">
						添加虚拟 Key
					</Button>
				</div>
			</div>
		</div>
	);
}
