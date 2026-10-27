import { Button } from "antd";

import { Callout } from "@/components/ui/product/callout";
import type { DramaSceneRuleNotice, DramaShotlistView, DramaStepOutcome } from "@/services/api/drama";

import { storyboardOutcomeSummary, type ShotStreamState, type StoryboardRunStop } from "./chapter-shot-stream";
import { DramaRunPanel } from "./drama-run-panel";

type ChapterStoryboardRunPanelProps = {
    open: boolean;
    onClose: () => void;
    chapterTitle: string;
    running: boolean;
    stage?: string;
    stream: ShotStreamState;
    outcome?: DramaStepOutcome;
    shotlist?: DramaShotlistView | null;
    /** 上一次任务失败或取消：写完 ≥1 场的给「从第 N 场继续」，否则给失败原因和「重新生成」。 */
    stop?: StoryboardRunStop;
    /** 本章当前的段数：摘要行按它说段数（重新装段后任务结果里的数字就旧了），「重新装段」也用它提示改动范围。 */
    segmentCount: number;
    onResume: () => void;
    onRepack: () => void;
    repacking: boolean;
    onRegenerate: () => void;
    onOpenProduction: () => void;
    /** 切场规则改变后，本章已有的镜头表 / 色彩脚本 / 色卡还是旧场次编号：提示重新生成。 */
    sceneNotice?: DramaSceneRuleNotice;
};

/**
 * 生成分段的进度侧栏（通用的 DramaRunPanel 加这一步的提示）：流式镜头按场分组实时显示；结束后给出写库结果。
 * 镜头表检查有 error 时服务端只存了草稿、没装段，这里列出问题行，处理后「重新装段」。
 * 镜头表已写入（非草稿）时也能「重新装段」：项目单段时长、资产或编译档案改了以后不调模型重装。
 */
export function ChapterStoryboardRunPanel({ open, onClose, chapterTitle, running, stage, stream, outcome, shotlist, stop, segmentCount, onResume, onRepack, repacking, onRegenerate, onOpenProduction, sceneNotice }: ChapterStoryboardRunPanelProps) {
    const draftErrors = shotlist?.draft ? shotlist.check.items.filter((item) => item.level === "error") : [];
    const parse = shotlist?.check.parse;
    const failed = stop?.kind === "failed" ? stop : undefined;
    const repackable = Boolean(shotlist && !shotlist.draft);
    const repackButton = (
        <Button size="small" loading={repacking} onClick={onRepack}>
            重新装段
        </Button>
    );
    const status = (
        <>
            {running ? (
                <>
                    <span className="font-medium text-foreground/80">{stream.total ? `正在写第 ${Math.max(1, stream.current)}/${stream.total} 场` : stream.shotCount ? "正在收尾" : "等待模型开始输出"}</span>
                    <span>·</span>
                    <span>已出 {stream.shotCount} 镜</span>
                    {stage ? (
                        <>
                            <span>·</span>
                            <span>{stage}</span>
                        </>
                    ) : null}
                </>
            ) : outcome ? (
                <span>{storyboardOutcomeSummary(outcome, { segmentCount, draft: shotlist?.draft })}</span>
            ) : stream.shotCount ? (
                <span>上一次输出 {stream.shotCount} 镜</span>
            ) : failed ? (
                <span>{failed.status === "cancelled" ? "上一次生成已取消" : "上一次生成失败"}</span>
            ) : (
                <span>还没有生成记录</span>
            )}
            {parse && !running ? (
                <span className="tabular-nums">
                    · 解析 {parse.rows} 行{parse.rejected ? `，${parse.rejected} 行没解析出来` : ""}
                </span>
            ) : null}
        </>
    );
    return (
        <DramaRunPanel
            open={open}
            onClose={onClose}
            title={`生成分段 · ${chapterTitle}`}
            running={running}
            status={status}
            unit="场"
            stepLabel="生成分段"
            stop={stop}
            onResume={onResume}
            onRegenerate={onRegenerate}
            failedNote={repackable ? "本章现有的段不受影响。" : undefined}
            followKey={stream.shotCount}
            emptyText="模型输出的镜头会按场出现在这里"
            listLabel="流式镜头"
            groups={stream.scenes.map((scene) => ({
                key: scene.index,
                title: `第 ${scene.index} 场`,
                heading: scene.heading,
                meta: `${scene.genre ? `${scene.genre} · ` : ""}${scene.shots.length} 镜${scene.attempt > 1 ? ` · 第 ${scene.attempt} 次` : ""}`,
                body: (
                    <ol className="space-y-1 pl-0">
                        {scene.shots.map((shot, index) => (
                            <li key={`${scene.index}-${index}`} className="list-none rounded-md border border-border/60 px-2.5 py-1.5 text-xs leading-5">
                                <div className="flex gap-2 text-[var(--fs-micro)] text-foreground/45">
                                    <span className="tabular-nums">#{shot.shotNumber}</span>
                                    {shot.size ? <span>{shot.size}</span> : null}
                                    {shot.id ? <span className="font-mono">{shot.id}</span> : null}
                                </div>
                                <div className="line-clamp-2 text-foreground/80">{shot.plot || "（没写画面）"}</div>
                                {shot.dialogue ? <div className="line-clamp-2 text-foreground/55">{shot.dialogue}</div> : null}
                            </li>
                        ))}
                    </ol>
                ),
            }))}
        >
            {sceneNotice && !running ? (
                <Callout
                    tone="warning"
                    title="场次规则已更新，建议重新生成"
                    action={
                        <Button size="small" onClick={onRegenerate}>
                            重新生成
                        </Button>
                    }
                >
                    {sceneNotice.message}
                </Callout>
            ) : null}
            {draftErrors.length ? (
                <Callout
                    tone="error"
                    title={`镜头表检查有 ${shotlist?.check.errors} 个问题，还没装段`}
                    action={
                        <div className="flex gap-1.5">
                            <Button size="small" onClick={onRegenerate}>
                                重新生成
                            </Button>
                            <Button size="small" type="primary" loading={repacking} onClick={onRepack}>
                                重新装段
                            </Button>
                        </div>
                    }
                >
                    <p className="mb-2">镜头表已按草稿保存，本章的段仍是上一次装出的。改完章节正文、项目时长、资产或编译档案后点「重新装段」；问题出在模型输出上就重新生成。</p>
                    <ul className="max-h-56 space-y-1 overflow-y-auto pl-0" aria-label="镜头表检查问题">
                        {draftErrors.map((item, index) => (
                            <li key={`${item.rule}-${item.shotId || "all"}-${index}`} className="list-none rounded-md bg-surface-active px-2.5 py-1.5">
                                <span className="font-medium text-foreground/85">{item.shotId || "整章"}</span>
                                <span className="ml-2 font-mono text-[var(--fs-micro)] text-foreground/50">{item.rule}</span>
                                <div>{item.message}</div>
                            </li>
                        ))}
                    </ul>
                </Callout>
            ) : null}

            {outcome?.status === "written" ? (
                <Callout
                    tone="success"
                    title="分段已写入分镜制作"
                    action={
                        <div className="flex gap-1.5">
                            {repackable ? repackButton : null}
                            <Button size="small" onClick={onOpenProduction}>
                                去分镜制作
                            </Button>
                        </div>
                    }
                >
                    {outcome.warnings ? `镜头表检查有 ${outcome.warnings} 条提醒，可在分镜制作页按段查看。` : "镜头表检查没有问题。"}
                    {outcome.notes?.length ? ` ${outcome.notes.join("；")}` : ""}
                    {repackable ? " 之后改了项目单段时长、资产或编译档案，点「重新装段」按当前设置重装，不调模型。" : ""}
                </Callout>
            ) : repackable ? (
                <Callout tone="info" title={`镜头表已写入，本章 ${segmentCount} 段`} action={repackButton}>
                    改了项目单段时长、资产或编译档案后，点「重新装段」用已存的镜头表按当前设置重装，不调模型；会整章替换段。
                </Callout>
            ) : null}
        </DramaRunPanel>
    );
}
