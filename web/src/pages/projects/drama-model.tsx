import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { configuredModelMatchesCapability, modelDisplayName, selectableModelsByCapability, useEffectiveConfig, type AiConfig } from "@/stores/use-config-store";
import { getDramaStyleProfiles, listDramaModels, recommendDramaStyleProfile, type DramaModel, type DramaPipelineStageKey, type DramaRegistry, type DramaRunMode, type DramaStyleProfiles } from "@/services/api/drama";

/** models.json 的默认值；目录还没读到时用它占位，最终以服务端校验为准。 */
export const DEFAULT_DRAMA_MODEL = "seedance-2.0";

/** 流水线的环节（与后端 drama.PipelineStages 同序）。 */
export const DRAMA_PIPELINE_STAGES: Array<{ key: DramaPipelineStageKey; label: string }> = [
    { key: "script", label: "拆剧本" },
    { key: "style", label: "导演定画风" },
    { key: "palette", label: "场次色卡" },
    { key: "assets", label: "角色场景资产" },
    { key: "shots", label: "分镜" },
    { key: "pack", label: "装段" },
    { key: "video", label: "出视频" },
    { key: "cut", label: "剪辑" },
];

/**
 * 三档生成模式（与后端 drama.ShouldAutoContinue 一致）。stops 是这一档在哪几格前停下等你点：
 * 出视频在任何模式下都停（要花钱），剪辑跟着出视频之后由人做。
 */
export const DRAMA_RUN_MODES: Array<{ value: DramaRunMode; label: string; hint: string; stops: DramaPipelineStageKey[] }> = [
    { value: "until_video", label: "跑到出视频前", hint: "从拆剧本一路自动做到装段完成，停在出视频前", stops: ["video"] },
    { value: "review_from_assets", label: "资产我要过目", hint: "自动跑到场次色卡；从资产提取起每一步都停下等你看", stops: ["assets", "shots", "video"] },
    { value: "every_step", label: "每步都问我", hint: "每一步做完都停下，等你点继续", stops: ["script", "style", "palette", "assets", "shots", "video"] },
];

/** 生成模式的中文名；不认识的按默认档显示。 */
export function dramaRunModeLabel(mode: string | undefined) {
    return (DRAMA_RUN_MODES.find((item) => item.value === mode) || DRAMA_RUN_MODES[0]).label;
}

/** 固定的一句：出视频要花钱，任何模式都在这里停。 */
export const DRAMA_VIDEO_STOP_NOTE = "出视频要花钱，任何模式都会在这里停下，带上预估花费等你点。";

export function useDramaModels() {
    return useQuery({ queryKey: ["drama", "models"], queryFn: async () => (await listDramaModels()).models, staleTime: 10 * 60_000 });
}

/** 五个风格档案与画风对照表（新建项目、项目设置的「风格档案」选项与默认推荐）。 */
export function useDramaStyleProfiles() {
    return useQuery({ queryKey: ["drama", "style-profiles"], queryFn: ({ signal }) => getDramaStyleProfiles(signal), staleTime: 10 * 60_000 });
}

/** 风格档案选项：按画风推荐的那个标「按画风推荐」。 */
export function dramaStyleProfileOptions(profiles: DramaStyleProfiles | undefined, recommended?: string) {
    return (profiles?.profiles || []).map((profile) => ({ value: profile.id, label: profile.id === recommended ? `${profile.label}（按画风推荐）` : profile.label }));
}

/** 按画风快照推荐风格档案；对照表没读到时返回 undefined。 */
export function recommendedDramaStyleProfile(profiles: DramaStyleProfiles | undefined, style: Parameters<typeof recommendDramaStyleProfile>[1]) {
    return profiles ? recommendDramaStyleProfile(profiles.styleMap, style) : undefined;
}

export const DRAMA_STYLE_PROFILE_HINT = "决定镜头表检查、装段的规则，以及生成分段用哪个流程技能；默认按画风推荐。";

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

/**
 * 每个生成模型族在当前账号已配置的视频渠道上支持哪些画幅（取渠道模型能力配置 video.ratios 的并集）。
 * 没有渠道的模型族不在表里，按「不知道」处理，不置灰。
 */
export function useDramaFamilyRatios(registry: DramaRegistry | undefined) {
    const config = useEffectiveConfig();
    return useMemo(() => {
        const ratios = new Map<string, Set<string>>();
        if (!registry) return ratios;
        for (const value of selectableModelsByCapability(config, "video")) {
            const family = dramaFamilyOf(registry, value, modelDisplayName(config, value));
            if (!family) continue;
            const supported = ratios.get(family.id) || new Set<string>();
            for (const ratio of modelCapabilityConfigFor(config, value).video?.ratios || []) supported.add(ratio);
            ratios.set(family.id, supported);
        }
        return ratios;
    }, [config, registry]);
}

/** 单段最长时长的两个档位（秒）；选了之后生成模型只列一段上限够得着的。 */
export const SEGMENT_SECONDS_OPTIONS = [15, 30] as const;

/** 没有可用渠道时优先选一个有渠道的模型族，避免建了项目却生成不了。 */
export function preferredDramaModel(registry: DramaRegistry | undefined, available: Set<string> | undefined) {
    const fallback = registry?.default || DEFAULT_DRAMA_MODEL;
    if (!registry || !available?.size || available.has(fallback)) return fallback;
    return registry.models.find((item) => available.has(item.id))?.id || fallback;
}

/**
 * 生成模型选项。minSeconds：只列一段上限 ≥ 它的模型（单段最长时长）；aspectRatio + ratios：渠道不支持该画幅的置灰并注明。
 */
export function dramaModelOptions(registry?: DramaRegistry, available?: Set<string>, filter?: { minSeconds?: number; aspectRatio?: string; ratios?: Map<string, Set<string>> }) {
    if (!registry?.models?.length) return [{ label: "Seedance 2.0 · 每段最长 15 秒", value: DEFAULT_DRAMA_MODEL, disabled: false }];
    return registry.models
        .filter((item) => !filter?.minSeconds || item.duration.max >= filter.minSeconds)
        .map((item) => {
            const ratios = filter?.aspectRatio ? filter.ratios?.get(item.id) : undefined;
            const ratioUnsupported = Boolean(ratios && filter?.aspectRatio && !ratios.has(filter.aspectRatio));
            const note = ratioUnsupported ? ` · 渠道不支持 ${filter?.aspectRatio}` : available && !available.has(item.id) ? " · 没有可用渠道" : "";
            return { label: `${item.label} · 每段最长 ${item.duration.max} 秒${note}`, value: item.id, disabled: ratioUnsupported };
        });
}

/**
 * 分镜制作页默认用的视频模型（与工作台一致）：项目默认视频模型优先，其次是与项目生成模型同族的渠道模型，
 * 最后是全局默认。章节装段要按它的时长档位取段长。
 */
export function projectVideoModel(config: AiConfig, projectDefault: string | undefined, registry: DramaRegistry | undefined, family: DramaModel | undefined) {
    if (projectDefault && configuredModelMatchesCapability(config, projectDefault, "video")) return projectDefault;
    const options = selectableModelsByCapability(config, "video");
    const sameFamily = family ? options.find((value) => dramaFamilyOf(registry, value, modelDisplayName(config, value))?.id === family.id) : undefined;
    return sameFamily || config.videoModel || options[0] || "";
}

export function findDramaModel(registry: DramaRegistry | undefined, id: string | undefined): DramaModel | undefined {
    if (!registry) return undefined;
    return registry.models.find((item) => item.id === (id || registry.default)) || registry.models.find((item) => item.id === registry.default);
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
