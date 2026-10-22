import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { modelDisplayName, selectableModelsByCapability, useEffectiveConfig } from "@/stores/use-config-store";
import { listDramaModels, type DramaAuthMode, type DramaModel, type DramaRegistry } from "@/services/api/drama";

/** models.json 的默认值；目录还没读到时用它占位，最终以服务端校验为准。 */
export const DEFAULT_DRAMA_MODEL = "seedance-2.0";

export const DRAMA_AUTH_OPTIONS: Array<{ label: string; value: DramaAuthMode; hint: string }> = [
    { label: "每步确认", value: "step", hint: "每个节点交付后停下来等你确认" },
    { label: "全权", value: "full", hint: "一路做到交付，只在超预算、删文件、对外发布、用你的账号时找你" },
];

export function useDramaModels() {
    return useQuery({ queryKey: ["drama", "models"], queryFn: async () => (await listDramaModels()).models, staleTime: 10 * 60_000 });
}

/** 当前账号已配置视频渠道覆盖到的生成模型族；目录没读到时返回 undefined（不做判断）。 */
export function useDramaChannelFamilies(registry: DramaRegistry | undefined) {
    const config = useEffectiveConfig();
    return useMemo(() => {
        if (!registry) return undefined;
        const families = new Set<string>();
        for (const value of selectableModelsByCapability(config, "video")) {
            const family = dramaFamilyOf(registry, value, modelDisplayName(config, value));
            if (family) families.add(family.id);
        }
        return families;
    }, [config, registry]);
}

/** 没有可用渠道时优先选一个有渠道的模型族，避免建了项目却生成不了。 */
export function preferredDramaModel(registry: DramaRegistry | undefined, available: Set<string> | undefined) {
    const fallback = registry?.default || DEFAULT_DRAMA_MODEL;
    if (!registry || !available?.size || available.has(fallback)) return fallback;
    return registry.models.find((item) => available.has(item.id))?.id || fallback;
}

export function dramaModelOptions(registry?: DramaRegistry, available?: Set<string>) {
    if (!registry?.models?.length) return [{ label: "Seedance 2.0 · 每段最长 15 秒", value: DEFAULT_DRAMA_MODEL }];
    return registry.models.map((item) => ({ label: `${item.label} · 每段最长 ${item.duration.max} 秒${available && !available.has(item.id) ? " · 没有可用渠道" : item.calibrated ? "" : " · 未校准"}`, value: item.id }));
}

export function findDramaModel(registry: DramaRegistry | undefined, id: string | undefined): DramaModel | undefined {
    if (!registry) return undefined;
    return registry.models.find((item) => item.id === (id || registry.default)) || registry.models.find((item) => item.id === registry.default);
}

export function dramaAuthLabel(mode: string | undefined) {
    return mode === "full" ? "全权" : "每步确认";
}

/** 渠道模型属于哪个生成模型族：按 models.json 的 match 片段取最长匹配，与后端 MatchChannelModel 一致。 */
export function dramaFamilyOf(registry: DramaRegistry | undefined, ...names: Array<string | undefined>): DramaModel | undefined {
    if (!registry) return undefined;
    for (const raw of names) {
        const name = (raw || "").trim().toLowerCase();
        if (!name) continue;
        let best: DramaModel | undefined;
        let bestLength = 0;
        for (const item of registry.models) {
            for (const fragment of item.match || []) {
                const value = fragment.toLowerCase();
                if (value.length > bestLength && name.includes(value)) {
                    best = item;
                    bestLength = value.length;
                }
            }
        }
        if (best) return best;
    }
    return undefined;
}
