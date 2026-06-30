import { describe, expect, it } from "vitest";

import type { LogEntry } from "@/lib/types/logs";

import { getMessage } from "./columns";

describe("getMessage", () => {
	it("returns text from input history", () => {
		const log = {
			object: "chat_completion",
			input_history: [
				{
					role: "user",
					content: [{ type: "text", text: "hello from the browser" }],
				},
			],
		} as unknown as LogEntry;

		expect(getMessage(log)).toBe("hello from the browser");
	});

	it("returns text from output message", () => {
		const log = {
			object: "chat_completion",
			input_history: [],
			responses_input_history: [],
			output_message: {
				role: "assistant",
				content: [{ type: "text", text: "hello from the model" }],
			},
		} as unknown as LogEntry;

		expect(getMessage(log)).toBe("hello from the model");
	});
});
