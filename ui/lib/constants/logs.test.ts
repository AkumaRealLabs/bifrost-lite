import { describe, expect, it } from "vitest";

import { RequestTypeColors, RequestTypeLabels, RequestTypes } from "./logs";

describe("logs constants", () => {
	it("does not register removed live request types", () => {
		const removedTypes = ["real" + "time.turn", "web" + "socket_responses"];
		for (const requestType of removedTypes) {
			expect(RequestTypes).not.toContain(requestType);
			expect(RequestTypeLabels).not.toHaveProperty(requestType);
			expect(RequestTypeColors).not.toHaveProperty(requestType);
		}
	});
});
