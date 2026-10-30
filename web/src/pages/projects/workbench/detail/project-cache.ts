import type { ProjectAsset, ProjectAssetCandidatePage, ProjectUnit, ProjectUnitWorkspace, ShotAssetReference } from "@/services/api/projects";

type ProjectUnitIndex = { units: ProjectUnit[]; canvasCounts: Record<string, number> };

/**
 * 新建、导入章节后先写进章节索引缓存再跳转：项目页的路由守卫按缓存判断章节是否存在，
 * 缓存还是旧的会把新章节重定向回第一章，看起来就像列表没刷新。
 */
export function appendProjectUnits(current: ProjectUnitIndex | undefined, units: ProjectUnit[]) {
    if (!current) return current;
    const known = new Set(current.units.map((unit) => unit.id));
    const added = units.filter((unit) => !known.has(unit.id));
    return added.length ? { ...current, units: [...current.units, ...added] } : current;
}

/** 确认后的候选立刻从待确认列表里拿掉，后台刷新再校正总数和分页。 */
export function removeConfirmedCandidate(current: ProjectAssetCandidatePage | undefined, candidateId: string) {
    if (!current || !current.candidates.some((candidate) => candidate.id === candidateId)) return current;
    return { ...current, candidates: current.candidates.filter((candidate) => candidate.id !== candidateId), total: Math.max(0, current.total - 1) };
}

/** 镜头绑定 / 取消绑定资产后立刻改本章工作区缓存，「镜头资产」不必等整章重取才出现。 */
export function applyShotReferenceChange(current: ProjectUnitWorkspace | undefined, change: { added?: ShotAssetReference; asset?: ProjectAsset; removedId?: string }) {
    if (!current) return current;
    if (change.removedId) return { ...current, shotReferences: current.shotReferences.filter((reference) => reference.id !== change.removedId) };
    const added = change.added;
    if (!added || current.shotReferences.some((reference) => reference.id === added.id)) return current;
    return { ...current, shotReferences: [...current.shotReferences, { ...added, asset: added.asset || change.asset }] };
}
