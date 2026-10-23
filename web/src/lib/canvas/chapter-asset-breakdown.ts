import { extractCharacterBreakdownJson, parseCharacterBreakdown, type CharacterBreakdown } from "./canvas-character-reference";

/** anchors 只有场景有：人物能站、能走到的位置（分镜站位用这些名字，镜头表检查 R-SHOT-012 拿它当空间图）。 */
export type ChapterAssetDefinition = { name: string; description: string; prompt: string; anchors?: string[] };
export type ChapterAssetBreakdown = { characters: CharacterBreakdown[]; scenes: ChapterAssetDefinition[]; props: ChapterAssetDefinition[] };

export function parseChapterAssetBreakdown(raw: string): ChapterAssetBreakdown {
    const text = raw.trim().replace(/^```(?:json)?\s*/i, "").replace(/\s*```$/i, "");
    const value: unknown = extractCharacterBreakdownJson(text, true);
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("章节资产结果必须是 JSON 对象");
    const payload = value as Record<string, unknown>;
    if (!Array.isArray(payload.characters) || !Array.isArray(payload.scenes) || !Array.isArray(payload.props)) {
        throw new Error("章节资产结果缺少角色、场景或道具数组，请重新提取");
    }
    const parseAssets = (items: unknown[], label: string): ChapterAssetDefinition[] => items.map((item) => {
        if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error(`${label}资产格式无效`);
        const asset = item as Record<string, unknown>;
        for (const key of ["name", "description", "prompt"]) {
            if (typeof asset[key] !== "string" || !asset[key].trim()) throw new Error(`${label}资产缺少有效的 ${key}`);
        }
        const anchors = label === "场景" ? sceneAnchorList(asset.anchors) : [];
        return { name: (asset.name as string).trim(), description: (asset.description as string).trim(), prompt: (asset.prompt as string).trim(), ...(anchors.length ? { anchors } : {}) };
    });
    return {
        characters: payload.characters.length ? parseCharacterBreakdown(JSON.stringify({ characters: payload.characters })) : [],
        scenes: parseAssets(payload.scenes, "场景"),
        props: parseAssets(payload.props, "道具"),
    };
}

/** 场景设定里的 anchors 读成去空、去重的字符串数组；也接受顿号、逗号分隔的一句话（与后端 sceneAnchorList 一致）。 */
export function sceneAnchorList(value: unknown): string[] {
    const items = Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : typeof value === "string" ? value.split(/[、，,；;\n]/) : [];
    return [...new Set(items.map((item) => item.trim()).filter(Boolean))];
}
