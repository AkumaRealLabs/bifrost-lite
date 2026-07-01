export type ProviderScoringRow = {
	provider: string;
	model: string;
	availabilityScore: number;
	ttfbScore: number;
	costScore: number;
	finalMultiplier: number;
	baseWeight: number;
	effectiveWeight: number;
	status: string;
};

export type ProviderScoringSummary = {
	rows: ProviderScoringRow[];
	failOpen: boolean;
	selectedProvider?: string;
	selectedModel?: string;
};

const numberPattern = "[-+]?\\d*\\.?\\d+";
const scorePattern = new RegExp(
	`Provider scoring (\\S+) model (\\S+): avail=(${numberPattern}) ttfb=(${numberPattern}) cost=(${numberPattern}) final=(${numberPattern}) weight (${numberPattern}) -> (${numberPattern})`,
);

const messageFromRoutingLine = (line: string) => {
	const match = line.match(/^\[\d+\]\s+\[[^\]]+\]\s+-\s+(.*)$/);
	return match ? match[1] : line;
};

export function parseProviderScoringLogs(logs: string): ProviderScoringSummary {
	const rows: ProviderScoringRow[] = [];
	const skipped = new Set<string>();
	const cooled = new Set<string>();
	let selectedProvider: string | undefined;
	let selectedModel: string | undefined;
	let failOpen = false;

	for (const rawLine of logs.split("\n")) {
		const message = messageFromRoutingLine(rawLine.trim());
		if (!message) continue;

		const score = message.match(scorePattern);
		if (score) {
			rows.push({
				provider: score[1],
				model: score[2],
				availabilityScore: Number(score[3]),
				ttfbScore: Number(score[4]),
				costScore: Number(score[5]),
				finalMultiplier: Number(score[6]),
				baseWeight: Number(score[7]),
				effectiveWeight: Number(score[8]),
				status: "candidate",
			});
			continue;
		}

		const cooldownSkipped = message.match(/Provider scoring: skipping cooled provider (\S+) for auto routing/);
		if (cooldownSkipped) {
			skipped.add(cooldownSkipped[1]);
			continue;
		}

		const cooledDown = message.match(/Provider scoring: provider (\S+) cooled down until/);
		if (cooledDown) {
			cooled.add(cooledDown[1]);
			continue;
		}

		const selected = message.match(/Selected provider (\S+) for model (\S+)/);
		if (selected) {
			selectedProvider = selected[1];
			selectedModel = selected[2];
			continue;
		}

		if (message.includes("Provider scoring: all providers cooled down; fail-open")) {
			failOpen = true;
		}
	}

	for (const row of rows) {
		if (row.provider === selectedProvider && (!selectedModel || row.model === selectedModel)) {
			row.status = "selected";
		} else if (failOpen && skipped.has(row.provider)) {
			row.status = "fail-open";
		} else if (skipped.has(row.provider)) {
			row.status = "cooldown skipped";
		} else if (cooled.has(row.provider)) {
			row.status = "cooldown";
		}
	}

	return { rows, failOpen, selectedProvider, selectedModel };
}