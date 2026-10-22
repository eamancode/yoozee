import type { TimelineClip } from "@/types/timeline";

const KIND_LABEL: Record<TimelineClip["kind"], string> = { video: "视频片段", image: "图片片段", audio: "音频片段", subtitle: "字幕", text: "文字" };

/** 时间线片段的显示名：文字 / 标题优先；`asset:<id>` 是时间线内部引用，不展示给人，退回类型名。 */
export function timelineClipLabel(clip: Pick<TimelineClip, "kind" | "text" | "title" | "directMedia" | "nodeId" | "id">) {
    const named = clip.text || clip.title || clip.directMedia?.title;
    if (named) return named;
    if (!clip.nodeId || clip.nodeId.startsWith("asset:")) return KIND_LABEL[clip.kind] || clip.id;
    return clip.nodeId;
}
