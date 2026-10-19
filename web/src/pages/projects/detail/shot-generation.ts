import type { VideoCapabilityConfig } from "@/lib/model-capabilities";

/**
 * 镜头视频的段时长：分段 JSON 的 dur 优先，其次是镜头时长；再按模型声明的时长档位取「不短于镜头」的最近一档，
 * 并受项目生成模型一段的上限约束。不能退回渠道默认时长（常见 15 秒）：5 秒的镜头会按 15 秒报价和提交。
 */
export function shotVideoSeconds(input: { specSeconds?: number; shotSeconds: number; profile?: VideoCapabilityConfig; segmentMaxSeconds?: number }) {
    // 分段表维护的段以 dur 为准，渠道档位不能改写它（保存时会把段改长）。
    if (input.specSeconds && input.specSeconds > 0) return input.specSeconds;
    const wanted = Math.max(1, Number.isFinite(input.shotSeconds) ? input.shotSeconds : 1);
    const cap = input.segmentMaxSeconds && input.segmentMaxSeconds > 0 ? input.segmentMaxSeconds : Number.POSITIVE_INFINITY;
    const duration = input.profile?.duration;
    if (!duration) return Math.min(Math.ceil(wanted), cap);
    if (duration.selection === "enum") {
        const values = (duration.values || []).filter((value) => value > 0).sort((left, right) => left - right);
        const allowed = values.filter((value) => value <= cap);
        const candidates = allowed.length ? allowed : values;
        if (!candidates.length) return Math.min(Math.ceil(wanted), cap);
        return candidates.find((value) => value >= wanted) ?? candidates[candidates.length - 1];
    }
    const min = duration.min || 1;
    const step = duration.step || 1;
    const max = Math.max(min, Math.min(duration.max || min, cap));
    const maxAligned = min + Math.floor((max - min) / step) * step;
    const stepped = min + Math.max(0, Math.ceil((wanted - min) / step)) * step;
    return Math.min(maxAligned, Math.max(min, stepped));
}

/** 生成按钮上的运行计时：排队时还没开始跑，不能把排队时间算成「已运行」。 */
export function shotTaskRunningLabel(task: { status: string; startedAt?: string; createdAt?: string }, now: number) {
    if (task.status === "queued") return "排队中";
    const startedAt = Date.parse(task.startedAt || task.createdAt || "");
    const totalSeconds = Math.max(0, Math.floor((now - (Number.isFinite(startedAt) ? startedAt : now)) / 1_000));
    return `已运行${Math.floor(totalSeconds / 60)}分钟${String(totalSeconds % 60).padStart(2, "0")}秒`;
}
