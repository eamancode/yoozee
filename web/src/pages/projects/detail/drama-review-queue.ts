import type { DramaSegmentProgress } from "@/services/api/drama";

/** 证据种类的中文名（段行 missingEvidence 有时是种类）。 */
export const EVIDENCE_LABELS: Record<string, string> = { faces: "人脸核对", frames: "抽帧核对", transcript: "台词转写" };

/** 待审的段：已生成、还没过审，跨章节；保持进度表的顺序（后端按章节顺序、章内按段顺序排好）。 */
export function pendingReviewSegments(segments: DramaSegmentProgress[]) {
    return segments.filter((segment) => segment.generated && !segment.reviewed);
}

/** 一段还差什么才算过审：没选定候选，或选定的候选缺哪几项证据。 */
export function reviewGap(segment: DramaSegmentProgress) {
    if (!segment.selectedArtifactId) return "还没选定一条候选";
    return segment.missingEvidence?.length ? `缺${segment.missingEvidence.map((kind) => EVIDENCE_LABELS[kind] || kind).join("、")}` : "待审";
}

/** 带问题的段：带着检查问题装段后还没改完的（跨章节，保持进度表的顺序）。 */
export function issueSegments(segments: DramaSegmentProgress[]) {
    return segments.filter((segment) => (segment.issues || 0) > 0);
}

/** 每章（单元）带问题的段数，章节信息行用。 */
export function issueSegmentsByUnit(segments: DramaSegmentProgress[] = []) {
    const out: Record<string, number> = {};
    for (const segment of issueSegments(segments)) out[segment.unitId] = (out[segment.unitId] || 0) + 1;
    return out;
}
