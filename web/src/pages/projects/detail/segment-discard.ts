import type { DramaSegmentDiscard } from "@/services/api/drama";
import type { ProjectShot, ShotArtifact } from "@/services/api/projects";

const ARTIFACT_LABELS: Array<[keyof DramaSegmentDiscard["artifacts"], string]> = [
    ["storyboard", "分镜图"],
    ["action_board", "预演"],
    ["video", "视频"],
];

/** 本章段上已有的产物（整章重新生成会一起删掉）：按段的位置排，失败的产物不算；没有产物的段不列。 */
export function chapterSegmentDiscards(shots: ProjectShot[], artifacts: ShotArtifact[], unitId: string): DramaSegmentDiscard[] {
    const counts = new Map<string, DramaSegmentDiscard["artifacts"]>();
    for (const artifact of artifacts) {
        if (artifact.status === "failed" || !ARTIFACT_LABELS.some(([kind]) => kind === artifact.type)) continue;
        const kind = artifact.type as keyof DramaSegmentDiscard["artifacts"];
        const current = counts.get(artifact.shotId) || {};
        current[kind] = (current[kind] || 0) + 1;
        counts.set(artifact.shotId, current);
    }
    return shots
        .filter((shot) => shot.unitId === unitId && counts.has(shot.id))
        .sort((a, b) => a.position - b.position)
        .map((shot) => ({ shotId: shot.id, segment: shot.title.trim().split(/\s+/)[0] || shot.title, label: shot.title, artifacts: counts.get(shot.id)! }));
}

/** 确认框里的一行：「S04（E01S02-P1）：分镜图 1、预演 1」；code 把单元内编号换成项目编号显示。 */
export function segmentDiscardLine(discard: DramaSegmentDiscard, code: (text: string) => string = (text) => text) {
    const items = ARTIFACT_LABELS.filter(([kind]) => discard.artifacts[kind]).map(([kind, label]) => `${label} ${discard.artifacts[kind]}`);
    return `${discard.segment}${discard.section ? `（${code(discard.section)}）` : ""}：${items.join("、")}`;
}
