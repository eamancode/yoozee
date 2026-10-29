import { Button } from "antd";
import { useEffect, useRef, type ReactNode } from "react";

import { AppDrawer } from "@/components/ui/product/app-drawer";
import { Callout } from "@/components/ui/product/callout";

import type { StoryboardRunStop } from "./chapter-shot-stream";
import { formatTime } from "./shared";

/** 侧栏里按轮分组的一组：生成分段是一场的镜头，剧本优化是一集的正文。 */
export type DramaRunGroup = {
    key: string | number;
    /** 「第 2 场」「第 1 集」。 */
    title: string;
    heading?: string;
    /** 右侧的计数（「6 镜 · 第 2 次」）。 */
    meta: string;
    body: ReactNode;
};

type DramaRunPanelProps = {
    open: boolean;
    onClose: () => void;
    title: string;
    running: boolean;
    /** 顶部一行状态（正在写第几轮、结果摘要）。 */
    status: ReactNode;
    /** 一轮叫什么（场、集）和这一步叫什么（生成分段、剧本优化），用在续写与失败提示里。 */
    unit: "场" | "节" | "集" | "轮";
    stepLabel: string;
    /** 上一次任务失败或取消：写完 ≥1 轮的给「从第 N 轮继续」，各轮都写完、收尾失败的给「重新写入」（onResume），否则给失败原因和「重新生成」。 */
    stop?: StoryboardRunStop;
    onResume: () => void;
    onRegenerate: () => void;
    /** 失败时「重新生成」按钮的文字（资产设计写「重新提取」）。 */
    regenerateLabel?: string;
    /** 失败提示后面补一句（如「本章现有的段不受影响。」）。 */
    failedNote?: string;
    /** 这一步自己的提示（写库结果、检查问题）。 */
    children?: ReactNode;
    groups: DramaRunGroup[];
    /** 内容变多时跟着滚到底（镜头数、正文行数）。 */
    followKey: number;
    emptyText: string;
    listLabel: string;
};

/** 失败原因后面还要接一句提示：原因没以句末标点收尾时补句号（服务端给用户看的原因会截掉上游明细连同句号）。 */
function sentence(text: string) {
    return /[。！？.!?]$/.test(text.trim()) ? text.trim() : `${text.trim()}。`;
}

/**
 * 逐轮续写的进度侧栏（生成分段、剧本优化共用）：按轮分组实时显示模型输出；任务中途失败时「从第 N 场 / 集继续」，
 * 第 1 轮就失败时显示原因、重新生成。写库结果和检查问题由各步骤作为 children 传进来。
 */
export function DramaRunPanel({ open, onClose, title, running, status, unit, stepLabel, stop, onResume, onRegenerate, regenerateLabel = "重新生成", failedNote, children, groups, followKey, emptyText, listLabel }: DramaRunPanelProps) {
    const listRef = useRef<HTMLDivElement>(null);
    const followRef = useRef(true);
    useEffect(() => {
        const list = listRef.current;
        if (list && running && followRef.current) list.scrollTop = list.scrollHeight;
    }, [running, followKey]);
    const resume = stop?.kind === "resume" ? stop : undefined;
    const failed = stop?.kind === "failed" ? stop : undefined;
    const superseded = stop?.kind === "superseded" ? stop : undefined;
    const refinalize = stop?.kind === "refinalize" ? stop : undefined;
    return (
        <AppDrawer open={open} onClose={onClose} placement="right" size="min(520px, 100vw)" mask={false} title={title}>
            <div className="flex h-full min-h-0 flex-col gap-3">
                <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 text-[var(--fs-tiny)] text-foreground/55" aria-live="polite">
                    {status}
                </div>

                {resume && !running ? (
                    <Callout
                        tone="warning"
                        title={`上一次写到第 ${resume.from} ${unit}停了`}
                        action={
                            <Button size="small" type="primary" onClick={onResume}>
                                从第 {resume.from} {unit}继续
                            </Button>
                        }
                    >
                        前 {resume.from - 1} {unit}（共 {resume.total} {unit}）已保存，接着写会带上它们的对话，不用重跑。{resume.error ? `原因：${sentence(resume.error)}` : ""}
                    </Callout>
                ) : null}

                {refinalize && !running ? (
                    <Callout
                        tone={refinalize.status === "cancelled" ? "warning" : "error"}
                        title={`上一次 ${refinalize.total} ${unit}都写完了，写入没成功`}
                        action={
                            <div className="flex flex-wrap gap-2">
                                <Button size="small" type="primary" onClick={onResume}>
                                    用已写好的 {refinalize.total} {unit}重新写入
                                </Button>
                                <Button size="small" onClick={onRegenerate}>
                                    全部{regenerateLabel}
                                </Button>
                            </div>
                        }
                    >
                        各{unit}输出都已保存，重新写入不再调模型。{refinalize.error ? `原因：${sentence(refinalize.error)}` : ""}
                        {failedNote ? ` ${failedNote}` : ""}
                    </Callout>
                ) : null}

                {failed && !running ? (
                    <Callout
                        tone={failed.status === "cancelled" ? "warning" : "error"}
                        title={failed.status === "cancelled" ? `上一次${stepLabel}已取消` : `上一次${stepLabel}失败`}
                        action={
                            <Button size="small" type="primary" onClick={onRegenerate}>
                                {regenerateLabel}
                            </Button>
                        }
                    >
                        {failed.error ? `原因：${sentence(failed.error)}` : `没有写完任何一${unit}。`}
                        {failedNote ? ` ${failedNote}` : ""}
                    </Callout>
                ) : null}

                {superseded && !running ? (
                    <Callout
                        tone="info"
                        title={`上一次重跑${superseded.status === "cancelled" ? "已取消" : "失败"}，当前结果来自 ${formatTime(superseded.resultAt)} 的运行`}
                        action={
                            <Button size="small" onClick={onRegenerate}>
                                重新生成
                            </Button>
                        }
                    >
                        重跑没有写进去，本章现有的结果没变。{superseded.status === "failed" && superseded.error ? `原因：${sentence(superseded.error)}` : ""}
                    </Callout>
                ) : null}

                {running ? null : children}

                <div
                    ref={listRef}
                    onScroll={(event) => {
                        const el = event.currentTarget;
                        followRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
                    }}
                    className="thin-scrollbar min-h-0 flex-1 space-y-3 overflow-y-auto"
                    aria-label={listLabel}
                >
                    {groups.map((group) => (
                        <section key={group.key} aria-label={group.title}>
                            <header className="sticky top-0 z-[1] flex items-center gap-2 bg-background py-1 text-xs">
                                <span className="font-semibold text-foreground/80">{group.title}</span>
                                {group.heading ? <span className="truncate text-foreground/60">{group.heading}</span> : null}
                                <span className="ml-auto shrink-0 tabular-nums text-foreground/40">{group.meta}</span>
                            </header>
                            {group.body}
                        </section>
                    ))}
                    {running && !groups.length ? <div className="py-8 text-center text-xs text-foreground/40">{emptyText}</div> : null}
                </div>
            </div>
        </AppDrawer>
    );
}
