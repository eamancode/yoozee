export const characterFields = [
    ["role", "剧情定位与人物关系"], ["aliases", "别名"], ["appearance", "稳定外貌"], ["physique", "身高、体型与体态"],
    ["clothing", "默认服装造型"], ["personality", "性格与表演基线"], ["props", "固定道具"],
    ["consistencyPrompt", "跨镜头一致性约束"], ["multiViewPrompt", "三视图补充约束"],
    ["voiceLanguage", "语言与口音"], ["voiceAge", "声音年龄感"], ["voiceTimbre", "音色气质"],
] as const;

export type CharacterForm = { name: string } & Record<(typeof characterFields)[number][0], string>;

export function characterDefinition(values: CharacterForm, current: Record<string, unknown> = {}) {
    // The editor owns only these fields; extraction evidence and identity belong to the saved definition.
    const definition: Record<string, unknown> = { ...current };
    for (const [key] of characterFields) definition[key] = values[key]?.trim() || "";
    definition.aliases = values.aliases?.split(/[，,]/).map((item) => item.trim()).filter(Boolean) || [];
    return definition;
}
