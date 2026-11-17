import type { DramaColorScriptEntry } from "@/services/api/project-drama";
import type { ProjectAsset, ProjectUnit } from "@/services/api/projects";

type PaletteGroup = {
    key: string;
    title: string;
    number?: number;
    assets: ProjectAsset[];
    entries: DramaColorScriptEntry[];
};

/** 集序来自当前剧集列表。旧色卡没有来源 ID 时，只保留它记录的场编号。 */
export function paletteSceneGroups(episodes: Pick<ProjectUnit, "id" | "title">[] | undefined, palettes: ProjectAsset[], script: DramaColorScriptEntry[]) {
    const current = new Map<string, PaletteGroup>();
    episodes?.forEach((episode, index) => {
        current.set(episode.id, { key: `episode:${episode.id}`, title: `第 ${index + 1} 集《${episode.title}》场次色卡`, number: index + 1, assets: [], entries: [] });
    });
    const outside: PaletteGroup = { key: "outside", title: episodes ? "历史色卡 · 来源不在当前剧集" : "剧集顺序未读到", assets: [], entries: [] };
    const legacy: PaletteGroup = { key: "legacy", title: "历史色卡 · 未记录来源剧集", assets: [], entries: [] };
    const groupFor = (unitId?: string) => unitId ? current.get(unitId) ?? outside : legacy;
    for (const asset of palettes) {
        if (!asset.palette?.master) groupFor(asset.palette?.entry?.unit_id).assets.push(asset);
    }
    for (const entry of script) groupFor(entry.unit_id).entries.push(entry);
    return [...current.values(), outside, legacy].filter((group) => group.assets.length || group.entries.length);
}
