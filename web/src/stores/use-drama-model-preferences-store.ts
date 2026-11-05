import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { getActiveUserScope, scopedLocalStorage } from "@/lib/user-scope";

export type DramaModelCapability = "text" | "image" | "video";
export type DramaRecentModels = Partial<Record<DramaModelCapability, string>>;
export const DRAMA_MODEL_PREFERENCES_KEY = "yingce:drama-recent-models";

export function normalizeDramaRecentModels(value: unknown): DramaRecentModels {
    if (!value || typeof value !== "object") return {};
    const record = value as Record<string, unknown>;
    return Object.fromEntries((["text", "image", "video"] as const).flatMap((kind) => typeof record[kind] === "string" && record[kind].trim() ? [[kind, record[kind].trim()]] : []));
}

export const useDramaModelPreferencesStore = create<{ models: DramaRecentModels; remember: (kind: DramaModelCapability, model: string, userScope?: string) => void }>()(persist((set) => ({
    models: {},
    remember: (kind, model, userScope) => {
        if (!model.trim() || userScope && userScope !== getActiveUserScope()) return;
        set((state) => state.models[kind] === model.trim() ? state : ({ models: { ...state.models, [kind]: model.trim() } }));
    },
}), {
    name: DRAMA_MODEL_PREFERENCES_KEY,
    storage: createJSONStorage(() => scopedLocalStorage),
    partialize: (state) => ({ models: state.models }),
    merge: (persisted, current) => ({ ...current, models: normalizeDramaRecentModels((persisted as { models?: unknown } | undefined)?.models) }),
}));

export function rememberAcceptedDramaTask(task: { userId?: string; type: string; model?: string; inputJson?: string; clientContext?: { domainProjectId?: string; chapterOperation?: string } }) {
    const record = (value: unknown): Record<string, unknown> => value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
    let input: Record<string, unknown>;
    try { input = record(JSON.parse(task.inputJson || "{}")); } catch { return; }
    if (!task.clientContext?.domainProjectId && !task.clientContext?.chapterOperation && !record(input.metadata).domainProjectId) return;
    const kind = task.type === "canvas_text" ? "text" : task.type === "canvas_image" ? "image" : task.type === "canvas_video" ? "video" : undefined;
    if (!kind) return;
    const config = record(input.config);
    const selected = task.model?.includes("::") ? task.model : config.channelId && (config.channelModelKey || config.model) ? `${config.channelId}::${config.channelModelKey || config.model}` : task.model;
    if (typeof selected === "string") useDramaModelPreferencesStore.getState().remember(kind, selected, task.userId);
}
