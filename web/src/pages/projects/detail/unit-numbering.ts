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
