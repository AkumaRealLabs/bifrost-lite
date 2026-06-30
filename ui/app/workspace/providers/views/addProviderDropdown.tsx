import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdownMenu";
import { PlusIcon, Settings2Icon } from "lucide-react";

interface AddProviderDropdownProps {
	onAddCustomProvider: () => void;
	disabled?: boolean;
	/** Optional: use compact trigger for empty state */
	variant?: "default" | "empty";
}

export function AddProviderDropdown({
	onAddCustomProvider,
	disabled = false,
	variant = "default",
}: AddProviderDropdownProps) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					variant="outline"
					size={variant === "empty" ? "default" : "sm"}
					data-testid="add-provider-btn"
					className={variant === "empty" ? "" : "w-full justify-start"}
					aria-label="新增 Provider"
					disabled={disabled}
				>
					<PlusIcon className="h-4 w-4" />
					{variant === "empty" ? <span>新增 Provider</span> : <div className="text-xs">新增 Provider</div>}
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent
				align="start"
				className="custom-scrollbar max-h-[min(70vh,24rem)] min-w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto"
				data-testid="add-provider-dropdown"
			>
				{/* Add New Provider > Custom provider... — used by E2E (add-provider-option-custom) */}
				<DropdownMenuItem data-testid="add-provider-option-custom" onSelect={onAddCustomProvider}>
					<Settings2Icon className="h-4 w-4" />
					<span>自定义 Provider...</span>
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
