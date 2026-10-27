import type { DramaSegmentProgress } from "@/services/api/drama";

/** 证据种类的中文名（段行 missingEvidence 有时是种类）。 */
export const EVIDENCE_LABELS: Record<string, string> = { faces: "人脸核对", frames: "抽帧核对", transcript: "台词转写" };

/** 待审的段：已生成、还没过审（Derive 的段行按「生成了、没过审」筛，跨章节，保持进度表的顺序）。 */
export function pendingReviewSegments(segments: DramaSegmentProgress[]) {
    return segments.filter((segment) => segment.generated && !segment.reviewed);
}

/** 一段还差什么才算过审：没选定候选，或选定的候选缺哪几项证据。 */
export function reviewGap(segment: DramaSegmentProgress) {
    if (!segment.selectedArtifactId) return "还没选定一条候选";
    return segment.missingEvidence?.length ? `缺${segment.missingEvidence.map((kind) => EVIDENCE_LABELS[kind] || kind).join("、")}` : "待审";
}
