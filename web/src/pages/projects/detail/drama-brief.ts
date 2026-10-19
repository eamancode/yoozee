import type { DramaBible } from "@/services/api/drama";

/** 需求文档的六项事实（契约 brief），概览终态 1「需求问透了」要求每项都是已确认或已授权。 */
export const BRIEF_FACTS = [
    { key: "purpose", label: "用途", placeholder: "这条片子给谁看、拿去做什么" },
    { key: "platform", label: "平台与画幅", placeholder: "例如：抖音竖屏 9:16" },
    { key: "duration", label: "时长", placeholder: "例如：单集 60 秒，共 10 集" },
    { key: "taste", label: "口味", placeholder: "参考片、调性、不要什么" },
    { key: "budget", label: "预算", placeholder: "积分或金额上限" },
    { key: "acceptance", label: "验收标准", placeholder: "怎样算交付合格" },
] as const;

export type BriefFactKey = (typeof BRIEF_FACTS)[number]["key"];

/** 只有「已确认」「默认，已授权」能拿去花钱，见 backend/internal/drama/brief.go 的 BriefStatuses。 */
export const BRIEF_STATUSES = ["已确认", "默认，待确认", "默认，已授权", "待答"] as const;

export type BriefFactDraft = { value: string; status: string };

export function briefFactsFromDocument(document: Record<string, unknown>) {
    return Object.fromEntries(BRIEF_FACTS.map(({ key }) => {
        const fact = document[key];
        if (fact && typeof fact === "object" && !Array.isArray(fact)) {
            const record = fact as Record<string, unknown>;
            return [key, { value: typeof record.value === "string" ? record.value : "", status: typeof record.status === "string" && record.status ? record.status : "默认，待确认" }];
        }
        return [key, { value: typeof fact === "string" ? fact : "", status: "默认，待确认" }];
    })) as Record<BriefFactKey, BriefFactDraft>;
}

/** 把表单里的六项写回文档：保留原有的 quote/date；值为空的事实整项去掉（契约不允许空 value）。 */
export function applyBriefFacts(document: Record<string, unknown>, facts: Record<BriefFactKey, BriefFactDraft>) {
    const next: Record<string, unknown> = { ...document };
    for (const { key } of BRIEF_FACTS) {
        const value = facts[key]?.value.trim() || "";
        if (!value) {
            delete next[key];
            continue;
        }
        const previous = document[key] && typeof document[key] === "object" && !Array.isArray(document[key]) ? document[key] as Record<string, unknown> : {};
        next[key] = { ...previous, value, status: facts[key].status };
    }
    return next;
}

export function briefConfirmedCount(facts: Record<BriefFactKey, BriefFactDraft>) {
    return BRIEF_FACTS.filter(({ key }) => facts[key]?.value.trim() && (facts[key].status === "已确认" || facts[key].status === "默认，已授权")).length;
}

/** 文本框里的 JSON 解析成对象；不是对象就报一句能看懂的话。 */
export function parseDocumentJson(text: string): Record<string, unknown> {
    let parsed: unknown;
    try {
        parsed = JSON.parse(text || "{}");
    } catch (error) {
        throw new Error(`JSON 格式不对：${error instanceof Error ? error.message : "无法解析"}`);
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("文档必须是一个 JSON 对象");
    return parsed as Record<string, unknown>;
}

export function bibleSummary(bible: DramaBible | undefined) {
    const characters = bible?.characters || [];
    const props = bible?.props || [];
    const locations = bible?.locations || [];
    const variants = characters.flatMap((character) => character.variants || []);
    const unchecked = variants.filter((variant) => !variant.checked).length + props.filter((prop) => !prop.checked).length + locations.filter((location) => !location.checked).length;
    return { characters: characters.length, props: props.length, locations: locations.length, unchecked };
}
