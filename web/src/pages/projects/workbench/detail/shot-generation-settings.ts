import type { ShotArtifact } from "@/services/api/projects";

export type ShotGenerationSettings = { model: string; aspectRatio?: string; resolution?: string; imageQuality?: string };

export function shotGenerationSettingsKey(userId: string, projectId: string, shotId: string, stage: string) {
    return ["project-shot-generation", userId, projectId, shotId, stage].map(encodeURIComponent).join(":");
}

export function parseShotGenerationSettings(value: string | null): ShotGenerationSettings | undefined {
    if (!value || value.length > 2048) return undefined;
    try {
        const parsed = JSON.parse(value);
        if (!parsed || typeof parsed.model !== "string" || !parsed.model.trim() || parsed.model.length > 512) return undefined;
        const text = (key: string) => (typeof parsed[key] === "string" && parsed[key].length <= 80 ? parsed[key] : undefined);
        return { model: parsed.model, aspectRatio: text("aspectRatio"), resolution: text("resolution"), imageQuality: text("imageQuality") };
    } catch {
        return undefined;
    }
}

export function restoreShotGenerationSettings(input: { stored?: ShotGenerationSettings; artifacts: ShotArtifact[]; defaultModel: string; available: (model: string) => boolean }): {
    settings: ShotGenerationSettings;
    source: "remembered" | "artifact" | "default";
    notice?: string;
} {
    if (input.stored && input.available(input.stored.model)) return { settings: input.stored, source: "remembered" as const };
    const artifacts = input.artifacts
        .filter((artifact) => artifact.status === "ready")
        .slice()
        .sort((a, b) => Number(b.selected) - Number(a.selected) || b.version - a.version);
    for (const artifact of artifacts) {
        const parsed = parseShotGenerationSettings(artifact.metadataJson);
        if (!parsed || !input.available(parsed.model)) continue;
        return { settings: parsed, source: "artifact" as const, notice: input.stored ? "上次选择的渠道已不可用，已沿用当前产物的渠道；请核对本次规格和费用。" : undefined };
    }
    return {
        settings: { model: input.available(input.defaultModel) ? input.defaultModel : "" },
        source: "default" as const,
        notice: input.stored || artifacts.length ? "上次生成的渠道不可用或未记录完整参数，当前显示项目默认；请核对模型、规格和费用。" : undefined,
    };
}
