import type { ProjectUnit } from "@/services/api/projects";

export type UnitKind = "chapter" | "episode";

/** 单元是原文章节还是剧集（kind 为空的老数据按章节算）。 */
export function unitKindOf(unit: Pick<ProjectUnit, "kind">): UnitKind {
    return unit.kind === "episode" ? "episode" : "chapter";
}

/** 章和集各自从 1 编号（剧集不接在章节后面连续编号）：按 position 排好，返回同一类单元和量词。 */
export function unitsOfKind<T extends Pick<ProjectUnit, "kind" | "position">>(units: T[], kind: UnitKind): { units: T[]; noun: "章" | "集"; label: "章节" | "剧集" } {
    return {
        units: units.filter((unit) => unitKindOf(unit) === kind).sort((left, right) => left.position - right.position),
        noun: kind === "episode" ? "集" : "章",
        label: kind === "episode" ? "剧集" : "章节",
    };
}

/**
 * 单元在项目场次表里的集号（与后端 styleBibleUnits 同口径）：有剧集时剧集按位置从 1 数，原文章节不在场次表里（0）；
 * 没有剧集时原文章节从 1 数。找不到这个单元时为 0。
 */
export function unitEpisodeNumber(units: Array<Pick<ProjectUnit, "id" | "kind" | "position">>, unitId: string) {
    const episodes = unitsOfKind(units, "episode").units;
    const numbered = episodes.length ? episodes : unitsOfKind(units, "chapter").units;
    return numbered.findIndex((unit) => unit.id === unitId) + 1;
}

// 单元内编号：镜头表、小节、台词 id 都按单元自己从第 1 集数（E01S02、E01S02-P1、E01S02-03、E01S02L05），E 后恒为 01。
const UNIT_CODE = /(?<![A-Za-z0-9])E01S(?=\d)/g;

/**
 * 把单元内编号换成项目编号（E 后是集号，和场次表、场次色卡一致）：第 2 集的 E01S02-P1 显示成 E02S02-P1；
 * 传一段文字（检查问题、提示）时里面出现的都换。只换显示：存下的镜头表、小节、色卡对应关系仍用单元内编号。
 * episode ≤ 1（第 1 集、不在场次表里的原文章节）原样返回。
 */
export function projectSceneCode(code: string, episode: number) {
    return episode > 1 ? code.replace(UNIT_CODE, `E${String(episode).padStart(2, "0")}S`) : code;
}
