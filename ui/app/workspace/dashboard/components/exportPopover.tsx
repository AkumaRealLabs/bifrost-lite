import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdownMenu";
import { buildCSV, downloadCSV } from "@/lib/utils/csv";
import { Download, FileSpreadsheet, FileText, Loader2 } from "lucide-react";
import { useCallback, useState } from "react";
import { type DashboardData, getCSVSections } from "../utils/exportUtils";

const PDF_TAB_LABELS = ["总览", "Provider 用量", "模型排行", "虚拟 Key 排行"];

interface ExportPopoverProps {
	getData: () => DashboardData;
	onPreloadData: () => Promise<void>;
	onPdfExport: () => Promise<HTMLElement[]>;
	onPdfExportDone: () => void;
}

export function ExportPopover({ getData, onPreloadData, onPdfExport, onPdfExportDone }: ExportPopoverProps) {
	const [exporting, setExporting] = useState(false);

	const handleCsvExport = useCallback(async () => {
		setExporting(true);
		try {
			await onPreloadData();
			const sections = getCSVSections(getData(), "all");
			const parts: string[] = [];
			for (const section of sections) {
				if (section.csv.rows.length === 0) continue;
				parts.push(`# ${section.name}`);
				parts.push(buildCSV(section.csv.headers, section.csv.rows));
				parts.push("");
			}
			if (parts.length > 0) {
				downloadCSV(parts.join("\n"), "dashboard-export");
			}
		} finally {
			setExporting(false);
		}
	}, [getData, onPreloadData]);

	const handlePdfExport = useCallback(async () => {
		setExporting(true);

		// Yield a frame so the spinner renders before heavy work starts
		await new Promise((r) => requestAnimationFrame(r));

		try {
			const { generatePdf } = await import("@/lib/utils/pdf");

			const elements = await onPdfExport();

			const sections = elements.map((element, i) => ({
				element,
				label: PDF_TAB_LABELS[i],
			}));

			await generatePdf(sections, "dashboard-export", {
				branding: {
					logoSrc: "/bifrost-logo.webp",
					text: "Powered by",
				},
			});
		} finally {
			onPdfExportDone();
			setExporting(false);
		}
	}, [onPdfExport, onPdfExportDone]);

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button variant="outline" size="default" disabled={exporting} data-testid="dashboard-export-trigger">
					{exporting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
					{exporting ? "正在导出..." : "导出"}
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end">
				<DropdownMenuItem onClick={handleCsvExport} data-testid="export-csv-item">
					<FileSpreadsheet className="h-4 w-4" />
					CSV
				</DropdownMenuItem>
				<DropdownMenuItem onClick={handlePdfExport} data-testid="export-pdf-item">
					<FileText className="h-4 w-4" />
					PDF
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
