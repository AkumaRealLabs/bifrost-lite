export const SYSTEM_POOL_LOW = "gpt_low";
export const SYSTEM_POOL_STABLE = "gpt_stable";

export function isSystemPoolVK(name?: string) {
	return name === SYSTEM_POOL_LOW || name === SYSTEM_POOL_STABLE;
}

export function getPriceRMBPerDao(description?: string): number | undefined {
	if (!description) return undefined;
	try {
		const value = JSON.parse(description).price_rmb_per_dao;
		return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
	} catch {
		return undefined;
	}
}

export function setPriceRMBPerDao(description: string | undefined, price: number | undefined): string {
	let metadata: Record<string, unknown> = {};
	if (description) {
		try {
			const parsed = JSON.parse(description);
			if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) metadata = parsed;
		} catch {}
	}
	if (price == null) delete metadata.price_rmb_per_dao;
	else metadata.price_rmb_per_dao = price;
	return Object.keys(metadata).length ? JSON.stringify(metadata) : "";
}

export function systemPoolForPrice(price?: number) {
	if (price == null || price <= 0) return "";
	if (price <= 0.1) return SYSTEM_POOL_LOW;
	if (price <= 0.25) return SYSTEM_POOL_STABLE;
	return "";
}

export function systemPoolMembershipLabel(price?: number) {
	if (price == null || price <= 0 || price > 0.25) return "未入池";
	if (price <= 0.1) return "低价池 + 稳定池";
	return "稳定池";
}

export function systemPoolLabel(pool?: string) {
	switch (pool) {
		case SYSTEM_POOL_LOW:
			return "低价池";
		case SYSTEM_POOL_STABLE:
			return "稳定池";
		default:
			return "未入池";
	}
}
