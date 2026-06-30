import { Button } from "@/components/ui/button";
import { DialogFooter } from "@/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { Validator } from "@/lib/utils/validation";
import { Save } from "lucide-react";

interface FormFooterProps {
	validator: Validator;
	label: string;
	onCancel: () => void;
	isLoading: boolean;
	isEditing: boolean;
	hasPermission?: boolean;
}

export default function FormFooter({ validator, label, onCancel, isLoading, isEditing, hasPermission = true }: FormFooterProps) {
	const isDisabled = isLoading || !validator.isValid() || !hasPermission;

	const getTooltipMessage = () => {
		if (!hasPermission) return "你没有执行此操作的权限";
		if (isLoading) return "正在保存...";
		return validator.getFirstError() || "请先修正校验错误";
	};

	return (
		<DialogFooter className="mt-4">
			<Button type="button" variant="outline" onClick={onCancel} disabled={isLoading}>
				Cancel
			</Button>
			<TooltipProvider>
				<Tooltip>
					<TooltipTrigger asChild>
						<span>
							<Button type="submit" disabled={isDisabled}>
								<Save className="h-4 w-4" />
								{isLoading ? "正在保存..." : isEditing ? `更新${label}` : `创建${label}`}
							</Button>
						</span>
					</TooltipTrigger>
					{isDisabled && (
						<TooltipContent>
							<p>{getTooltipMessage()}</p>
						</TooltipContent>
					)}
				</Tooltip>
			</TooltipProvider>
		</DialogFooter>
	);
}
