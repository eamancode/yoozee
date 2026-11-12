import { projectLockedStyle } from "@/pages/projects/workbench/detail/chapter-style-lock";
import type { DramaBible } from "@/services/api/project-drama";

import { queryReadState } from "./read-state";

/**
 * 画风锁定状态。锁定口径和旧工作台、后端「风格定了」同一个：`projectLockedStyle().locked`，
 * 即风格锚非空且主基调色卡至少一个颜色。只有风格锚（草稿、手改的、缺色卡的）不算锁定。
 * 风格档案没读到（含带旧缓存的刷新失败）时是 error，不当成「没锁」也不当成「已锁」。
 */
export type StyleLockState = "loading" | "error" | "none" | "anchor_only" | "locked";

export function styleLockState(query: { isError: boolean; data?: DramaBible | null }): StyleLockState {
    const read = queryReadState(query);
    if (read !== "ready") return read;
    const view = projectLockedStyle(JSON.stringify(query.data ?? {}));
    if (view.locked) return "locked";
    return view.anchor && Object.keys(view.anchor).length ? "anchor_only" : "none";
}

export const STYLE_LOCK_TEXT: Record<StyleLockState, string> = {
    loading: "…",
    error: "没读到",
    none: "还没锁定",
    anchor_only: "有风格锚，还没锁定",
    locked: "已锁定",
};
