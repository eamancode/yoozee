import { Button } from "antd";

import { Callout } from "@/components/ui/product/callout";
import type { DramaScriptAdaptOutcome } from "@/services/api/project-drama";

import type { StoryboardRunStop } from "./chapter-shot-stream";
import { DramaRunPanel } from "./drama-run-panel";
import { SCRIPT_ADAPT_CONCEPT_TURN, scriptAdaptGroupTitle, scriptAdaptRunningText, type ScriptAdaptStreamState } from "./script-adapt-stream";

type ScriptAdaptRunPanelProps = {
    open: boolean;
    onClose: () => void;
    running: boolean;
    stage?: string;
    stream: ScriptAdaptStreamState;
    /** 断点、进度的单位：普通流水线按集；SSS 流水线第 1 轮是立意与全集结构，按轮。 */
    unit?: "集" | "轮";
    outcome?: DramaScriptAdaptOutcome;
    stop?: StoryboardRunStop;
    onResume: () => void;
    onRegenerate: () => void;
    onOpenEpisodes: () => void;
};

/** 剧本优化的进度侧栏：按集流式显示剧本正文；结束后给出写了几集、检查提醒。 */
export function ScriptAdaptRunPanel({ open, onClose, running, stage, stream, unit = "集", outcome, stop, onResume, onRegenerate, onOpenEpisodes }: ScriptAdaptRunPanelProps) {
    const failed = stop?.kind === "failed" ? stop : undefined;
    const rhythm = outcome?.mode === "rhythm";
    const conceptDone = stream.episodes.some((episode) => episode.turn === SCRIPT_ADAPT_CONCEPT_TURN);
    const episodeCount = stream.episodes.filter((episode) => episode.turn !== SCRIPT_ADAPT_CONCEPT_TURN).length;
    const status = running ? (
        <>
            <span className="font-medium text-foreground/80">{scriptAdaptRunningText(stream)}</span>
            <span>·</span>
            <span>已出 {stream.sceneCount} 场</span>
            {stage ? (
                <>
                    <span>·</span>
                    <span>{stage}</span>
                </>
            ) : null}
        </>
    ) : outcome ? (
        <span>
            {rhythm ? `已改写 ${outcome.episodes.length} 集` : `已写入 ${outcome.episodes.length} 集剧集`}
            {outcome.replaced?.length ? `，替换了原来的 ${outcome.replaced.length} 集` : ""}
        </span>
    ) : episodeCount ? (
        <span>
            上一次输出 {episodeCount} 集{conceptDone ? "（另有立意与全集结构）" : ""}
        </span>
    ) : conceptDone ? (
        <span>上一次只写完立意与全集结构</span>
    ) : failed ? (
        <span>{failed.status === "cancelled" ? "上一次剧本优化已取消" : "上一次剧本优化失败"}</span>
    ) : (
        <span>还没有剧本优化记录</span>
    );
    const warnings = (outcome?.items || []).filter((item) => item.level !== "error");
    return (
        <DramaRunPanel
            open={open}
            onClose={onClose}
            title="剧本优化"
            running={running}
            status={status}
            unit={unit}
            stepLabel="剧本优化"
            stop={stop}
            onResume={onResume}
            onRegenerate={onRegenerate}
            failedNote="原文和已有剧集都没有改动。"
            followKey={stream.episodes.reduce((sum, episode) => sum + episode.lines.length, 0)}
            emptyText="模型写出的剧本会按集出现在这里"
            listLabel="流式剧本"
            groups={stream.episodes.map((episode) => {
                const title = scriptAdaptGroupTitle(episode, stream);
                const concept = episode.turn === SCRIPT_ADAPT_CONCEPT_TURN;
                return {
                    key: episode.index,
                    title,
                    heading: episode.title && episode.title !== title && episode.title !== `第 ${episode.index} 集` ? episode.title : undefined,
                    meta: `${concept ? "立意页 · 全集结构表" : `${episode.scenes.length} 场`}${episode.attempt > 1 ? ` · 第 ${episode.attempt} 次` : ""}`,
                    body: (
                        <div className="space-y-0.5 rounded-md border border-border/60 px-2.5 py-1.5 text-xs leading-5">
                            {episode.lines.map((line, index) =>
                                line.startsWith("## ") ? (
                                    <div key={index} className="pt-1 font-medium text-foreground/80">
                                        {line.slice(3)}
                                    </div>
                                ) : (
                                    <div key={index} className={line.startsWith("（") ? "text-foreground/50" : "text-foreground/75"}>
                                        {line}
                                    </div>
                                ),
                            )}
                            {!episode.lines.length ? <div className="text-foreground/40">{concept ? "等待立意页和全集结构表…" : "等待这一集的正文…"}</div> : null}
                            {episode.design?.length ? (
                                <details>
                                    <summary>本集设计与台词定稿</summary>
                                    <div className="whitespace-pre-wrap">{episode.design.join("\n")}</div>
                                </details>
                            ) : null}
                        </div>
                    ),
                };
            })}
        >
            {outcome ? (
                <Callout
                    tone="success"
                    title={rhythm ? "剧集已按新节奏改写" : "剧集已写入"}
                    action={
                        <Button size="small" type="primary" onClick={onOpenEpisodes}>
                            看剧集
                        </Button>
                    }
                >
                    {warnings.length ? `检查有 ${warnings.length} 条提醒，台词写法不对的行生成分段会当成动作：` : "检查没有问题。提取资产、生成分段在剧集上照常用。"}
                    {outcome.chained?.taskId ? <div className="mt-1">接着生成场次色卡…（进度在「色卡进度」里）</div> : null}
                    {outcome.chained?.error ? <div className="mt-1">场次色卡没有自动开始：{outcome.chained.error}。锁定风格后可以在章节页点「生成场次色卡」。</div> : null}
                    {warnings.length ? (
                        <ul className="mt-2 max-h-48 space-y-1 overflow-y-auto pl-0" aria-label="剧本检查提醒">
                            {warnings.map((item, index) => (
                                <li key={`${item.rule}-${index}`} className="list-none rounded-md bg-surface-active px-2.5 py-1.5">
                                    <span className="font-medium text-foreground/85">{item.shotId || "全部"}</span>
                                    <span className="ml-2 font-mono text-[var(--fs-micro)] text-foreground/50">{item.rule}</span>
                                    <div>{item.message}</div>
                                </li>
                            ))}
                        </ul>
                    ) : null}
                </Callout>
            ) : null}
        </DramaRunPanel>
    );
}
