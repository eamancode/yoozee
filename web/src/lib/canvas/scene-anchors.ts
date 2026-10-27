/** 场景设定里的 anchors 读成去空、去重的字符串数组；也接受顿号、逗号分隔的一句话（与后端 sceneAnchorList 一致）。 */
export function sceneAnchorList(value: unknown): string[] {
    const items = Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : typeof value === "string" ? value.split(/[、，,；;\n]/) : [];
    return [...new Set(items.map((item) => item.trim()).filter(Boolean))];
}
