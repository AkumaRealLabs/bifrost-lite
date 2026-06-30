import { SheetNavigationButtons } from "@/components/sheetNavigationButtons";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { useGetLogByIdQuery } from "@/lib/store/apis/logsApi";
import type { LogEntry } from "@/lib/types/logs";
import { useSheetNavigation } from "@/hooks/useSheetNavigation";
import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { LogDetailView } from "./logDetailView";

interface LogDetailSheetProps {
	log: LogEntry | null;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	handleDelete?: (log: LogEntry) => void;
	onNavigate?: (direction: "prev" | "next") => void;
	hasPrev?: boolean;
	hasNext?: boolean;
	onViewSession?: (sessionId: string, logId: string) => void;
	onFilterByParentRequestId?: (parentRequestId: string) => void;
}

export function LogDetailSheet({
	log,
	open,
	onOpenChange,
	handleDelete,
	onNavigate,
	hasPrev = false,
	hasNext = false,
	onViewSession,
	onFilterByParentRequestId,
}: LogDetailSheetProps) {
	const [pollingInterval, setPollingInterval] = useState(0);
	const {
		data: fullLog,
		isLoading,
		isError,
	} = useGetLogByIdQuery(log?.id ?? "", {
		skip: !open || !log?.id,
		pollingInterval,
	});

	const shouldPoll = isError || fullLog?.status === "processing";

	const isFullDataReady = log != null && (isError || (fullLog?.id === log.id && !isLoading));
	useEffect(() => {
		setPollingInterval(shouldPoll ? 2000 : 0);
	}, [shouldPoll]);

	// Keyboard navigation: arrow up/down to navigate between logs
	const { prev: prevKeys, next: nextKeys } = useSheetNavigation({
		enabled: open,
		hasPrev,
		hasNext,
		onNavigate: (direction) => onNavigate?.(direction),
	});

	if (!log) return null;

	// Show a loader only on the initial fetch, not during background polling refetches.
	const displayLog: LogEntry = isFullDataReady && fullLog ? fullLog : log;

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="border-secondary flex w-full flex-col gap-4 overflow-x-hidden border p-8 sm:max-w-[60%]">
				{!isFullDataReady ? (
					<div className="flex h-full items-center justify-center">
						<SheetTitle className="sr-only">正在加载日志详情</SheetTitle>
						<Loader2 className="text-muted-foreground h-6 w-6 animate-spin" />
					</div>
				) : (
					<LogDetailView
						log={displayLog}
						resolvedSelectedPromptName={displayLog.selected_prompt_name ?? ""}
						handleDelete={handleDelete}
						onClose={() => onOpenChange(false)}
						onFilterByParentRequestId={onFilterByParentRequestId}
						headerAction={
							<>
								{displayLog.parent_request_id && onViewSession ? (
									<Button
										variant="outline"
										size="sm"
										data-testid="session-button-view"
										onClick={() => onViewSession(displayLog.parent_request_id as string, displayLog.id)}
									>
										查看会话
									</Button>
								) : null}
								<SheetNavigationButtons
									hasPrev={hasPrev}
									hasNext={hasNext}
									onNavigate={(dir) => onNavigate?.(dir)}
									prevKeys={prevKeys}
									nextKeys={nextKeys}
									entityLabel="日志"
								/>
							</>
						}
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}
