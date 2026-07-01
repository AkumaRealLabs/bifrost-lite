import { describe, expect, it } from "vitest";

import { parseProviderScoringLogs } from "./routingScoring";

describe("parseProviderScoringLogs", () => {
	it("parses scoring rows, cooldown skips, fail-open, and selected provider", () => {
		const logs = [
			"[1780000000000] [governance] - Provider scoring provider-alpha model gpt-4o: avail=0.90 ttfb=0.80 cost=1.00 final=0.88 weight 1.00 -> 0.88",
			"[1780000000001] [governance] - Provider scoring provider-beta model gpt-4o: avail=0.20 ttfb=0.50 cost=0.70 final=0.33 weight 1.00 -> 0.33",
			"[1780000000002] [governance] - Provider scoring: skipping cooled provider provider-beta for auto routing",
			"[1780000000003] [governance] - Provider scoring: all providers cooled down; fail-open using composite scores",
			"[1780000000004] [governance] - Selected provider provider-alpha for model gpt-4o (base_weight=1.00 effective_weight=0.88 from 2 eligible: [provider-alpha provider-beta])",
		].join("\n");

		const parsed = parseProviderScoringLogs(logs);

		expect(parsed.failOpen).toBe(true);
		expect(parsed.selectedProvider).toBe("provider-alpha");
		expect(parsed.rows).toEqual([
			{
				provider: "provider-alpha",
				model: "gpt-4o",
				availabilityScore: 0.9,
				ttfbScore: 0.8,
				costScore: 1,
				finalMultiplier: 0.88,
				baseWeight: 1,
				effectiveWeight: 0.88,
				status: "selected",
			},
			{
				provider: "provider-beta",
				model: "gpt-4o",
				availabilityScore: 0.2,
				ttfbScore: 0.5,
				costScore: 0.7,
				finalMultiplier: 0.33,
				baseWeight: 1,
				effectiveWeight: 0.33,
				status: "fail-open",
			},
		]);
	});
});