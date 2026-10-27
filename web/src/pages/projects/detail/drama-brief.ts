import type { DramaBible, DramaPrevizGate } from "@/services/api/drama";

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

export const dramaBriefKey = (projectId: string) => ["project", projectId, "drama", "brief"] as const;

/** 每集时长（秒）存在需求文档的 episode_seconds：生成分镜的提示词和后端 R-SEG-008 都读它。 */
export const EPISODE_SECONDS_RANGE = { min: 5, max: 3600 } as const;

export function briefEpisodeSeconds(document: Record<string, unknown> | undefined) {
    const value = document?.episode_seconds;
    return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
}

/**
 * 写回每集时长；空值去掉这一项。生成模型和授权档位以项目字段为准，不随需求文档写回：
 * 读到的 video_model / auth_mode 可能比刚保存的项目设置旧，带着写回会把项目字段改回去。
 */
export function withEpisodeSeconds(document: Record<string, unknown>, seconds: number | null | undefined) {
    const next: Record<string, unknown> = { ...document };
    delete next.video_model;
    delete next.auth_mode;
    if (seconds && seconds > 0) next.episode_seconds = Math.round(seconds);
    else delete next.episode_seconds;
    return next;
}

/** 单段最长秒数存在需求文档的 segment_seconds（建项目选 15 / 30）：装段时只收紧生成模型一段的上限。 */
export function briefSegmentSeconds(document: Record<string, unknown> | undefined) {
    const value = document?.segment_seconds;
    return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
}

/**
 * 一段最长秒数：生成模型上限再叠项目 segment_seconds，取更严的那个（同后端 briefSegmentRule，只收紧不放宽）。
 * modelMaxSeconds 为 0 表示模型没限；byBrief 表示上限来自项目设置，文案要说「项目单段」而不是模型。
 */
export function segmentMaxSecondsWithBrief(modelMaxSeconds: number, document: Record<string, unknown> | undefined) {
    const brief = briefSegmentSeconds(document);
    const seconds = brief ? Math.floor(brief) : 0;
    if (seconds >= 1 && (modelMaxSeconds <= 0 || seconds < modelMaxSeconds)) return { seconds, byBrief: true };
    return { seconds: modelMaxSeconds, byBrief: false };
}

/** 本地副本里换上新的单段秒数（真正写库走 updateProject 的 segmentSeconds，后端按生成模型上限校验）。 */
export function withSegmentSeconds(document: Record<string, unknown>, seconds: number | null | undefined) {
    const next: Record<string, unknown> = { ...document };
    if (seconds && seconds > 0) next.segment_seconds = Math.round(seconds);
    else delete next.segment_seconds;
    return next;
}

/** 视频前的预演闸门（brief_json.previz_gate）；没写或不认识的按 auto（跟授权档位）。 */
export function briefPrevizGate(document: Record<string, unknown> | undefined): DramaPrevizGate {
    const value = document?.previz_gate;
    return value === "on" || value === "off" ? value : "auto";
}

/** 把预演闸门写回需求文档；auto 时去掉这一项（同后端 withBriefPrevizGate）。 */
export function withPrevizGate(document: Record<string, unknown>, gate: DramaPrevizGate) {
    const next: Record<string, unknown> = { ...document };
    if (gate === "auto") delete next.previz_gate;
    else next.previz_gate = gate;
    return next;
}

/** 预演闸门的选项：auto 跟授权档位。 */
export const PREVIZ_GATE_OPTIONS: Array<{ value: DramaPrevizGate; label: string }> = [
    { value: "auto", label: "跟授权档位（每步确认时开）" },
    { value: "on", label: "开：先选中黑白预演再生成视频" },
    { value: "off", label: "关：直接生成视频" },
];

/** 只有「已确认」「默认，已授权」能拿去花钱，见 backend/internal/drama/brief.go 的 BriefStatuses。 */
export const BRIEF_STATUSES = ["已确认", "默认，待确认", "默认，已授权", "待答"] as const;

export type BriefFactDraft = { value: string; status: string };

export function briefFactsFromDocument(document: Record<string, unknown>) {
    return Object.fromEntries(
        BRIEF_FACTS.map(({ key }) => {
            const fact = document[key];
            if (fact && typeof fact === "object" && !Array.isArray(fact)) {
                const record = fact as Record<string, unknown>;
                return [key, { value: typeof record.value === "string" ? record.value : "", status: typeof record.status === "string" && record.status ? record.status : "默认，待确认" }];
            }
            return [key, { value: typeof fact === "string" ? fact : "", status: "默认，待确认" }];
        }),
    ) as Record<BriefFactKey, BriefFactDraft>;
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
        const previous = document[key] && typeof document[key] === "object" && !Array.isArray(document[key]) ? (document[key] as Record<string, unknown>) : {};
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
