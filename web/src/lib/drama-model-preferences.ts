import { configuredModelMatchesCapability, modelOptionName, selectableModelsByCapability, type AiConfig } from "@/stores/use-config-store";
import type { DramaModelCapability, DramaRecentModels } from "@/stores/use-drama-model-preferences-store";
import { configuredModelDisplayName } from "@/lib/model-selection";

export function preferredDramaModel(config: AiConfig, kind: DramaModelCapability, recent: DramaRecentModels, fallback?: string, accepts: (model: string) => boolean = () => true) {
    const available = selectableModelsByCapability(config, kind);
    const valid = (value?: string): value is string => Boolean(value && configuredModelMatchesCapability(config, value, kind) && accepts(value));
    const candidates = [recent[kind], ...(kind === "text" ? available.filter((value) => modelOptionName(value).toLowerCase() === "gpt-6.1-sol" || configuredModelDisplayName(config, value).toLowerCase() === "gpt-6.1-sol") : []), fallback, config[`${kind}Model`], ...available];
    return candidates.find(valid) || "";
}
