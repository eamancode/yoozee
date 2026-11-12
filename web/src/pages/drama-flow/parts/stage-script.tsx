import { useMemo, useState } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input } from "antd";
import { Pencil } from "lucide-react";

import { plainTextToHtml } from "@/lib/project-source";
import { briefEpisodeSeconds } from "@/pages/projects/workbench/detail/drama-brief";
import { ScriptAdaptControl } from "@/pages/projects/workbench/detail/chapter-script-adapt";
import { followChainedScenePalette } from "@/pages/projects/workbench/detail/chapter-scene-palette";
import { withEpisodeSourcesComment } from "@/pages/projects/workbench/detail/episode-sources";
import { isScriptAdaptTask } from "@/pages/projects/workbench/detail/project-chapter-ai";
import { unitEpisodeNumber } from "@/pages/projects/workbench/detail/unit-numbering";
import { dramaScenesKey, dramaScriptAdaptOutcomeOf, dramaSceneCode, getDramaScenes } from "@/services/api/project-drama";
import { getProjectUnit, updateProjectUnit, type ProjectUnit } from "@/services/api/projects";

import type { FlowData } from "../hooks/use-flow-data";
import type { FlowStageView } from "../model/stages";
import { ReadError, StageLayout, StageTabs, SummaryRow } from "./layout";

/** 剧本正文存的是 HTML（带一条来源注释）：编辑时只给纯文本，保存时再包回去并保留来源注释。 */
export function scriptHtmlToText(html: string) {
    return html
        .replace(/<!--[\s\S]*?-->/g, "")
        .replace(/<br\s*\/?>/gi, "\n")
        .replace(/<\/(p|div|h[1-6]|li)>/gi, "\n")
        .replace(/<[^>]+>/g, "")
        .replace(/&nbsp;/g, " ")
        .replace(/&lt;/g, "<")
        .replace(/&gt;/g, ">")
        .replace(/&quot;/g, '"')
        .replace(/&#39;/g, "'")
        .replace(/&amp;/g, "&")
        .replace(/\n{3,}/g, "\n\n")
        .trim();
}

/** 来源章节的第 from 到第 to 段（只数非空行，从 1 数）。 */
export function sourceParagraphs(chapterText: string, from: number, to: number) {
    const lines = scriptHtmlToText(chapterText)
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean);
    return lines.slice(Math.max(0, from - 1), to > 0 ? to : undefined);
}

type ScriptTab = "episodes" | "source" | "check";

export function StageScript({ data, stage, tab, onTab, onSelectUnit, onRedoShots }: { data: FlowData; stage?: FlowStageView; tab?: string; onTab: (tab: ScriptTab) => void; onSelectUnit: (unitId: string) => void; onRedoShots: (unitId: string) => void }) {
    const { detail, unit, fullUnit, workspaceQuery, unitsQuery, rail, episodes, episodesQuery, brief, projectTasks, chapters, episodeUnits, allUnits, project, dramaRule } = data;
    const active: ScriptTab = tab === "source" || tab === "check" ? tab : "episodes";
    const adaptTasks = useMemo(() => projectTasks.filter(isScriptAdaptTask), [projectTasks]);
    const lastOutcome = useMemo(() => {
        for (const task of adaptTasks) {
            const outcome = task.status === "succeeded" ? dramaScriptAdaptOutcomeOf(task) : undefined;
            if (outcome) return outcome;
        }
        return undefined;
    }, [adaptTasks]);
    const episodeRows = episodes?.episodes || [];
    const totalWords = episodeRows.reduce((sum, row) => sum + row.wordCount, 0);
    const withShots = episodeRows.filter((row) => row.shots > 0).length;
    const perEpisode = briefEpisodeSeconds(brief);
    const checkItems = lastOutcome?.items || [];

    if (!project || !detail) return null;
    return (
        <StageLayout>
            <div className="drama-flow-work-head">
                <div>
                    <h2>剧本</h2>
                    <p>把原文改成一集一集的剧本；每集的场次、正文和来源都在这里，直接改。</p>
                </div>
                <div className="drama-flow-work-actions">
                    <ScriptAdaptControl
                        detail={detail}
                        chapters={chapters}
                        episodes={episodeUnits}
                        tasks={projectTasks}
                        onWritten={(outcome) => outcome.episodes[0] && onSelectUnit(outcome.episodes[0].unitId)}
                        onChained={(taskId) => followChainedScenePalette(project.id, taskId)}
                    />
                </div>
            </div>
            {episodesQuery.isError ? <ReadError what="剧集表" onRetry={() => void episodesQuery.refetch()} /> : null}
            {unitsQuery.isError ? <ReadError what="剧集列表" onRetry={() => void unitsQuery.refetch()} /> : null}
            <SummaryRow
                items={[
                    { label: "剧集", value: episodes ? episodeRows.length : episodesQuery.isError ? "没读到" : "…" },
                    { label: "总字数", value: episodes ? totalWords.toLocaleString("zh-CN") : episodesQuery.isError ? "没读到" : "…" },
                    { label: "每集时长", value: perEpisode ? `${perEpisode} 秒` : "没设" },
                    { label: "原文章节", value: unitsQuery.isError ? "没读到" : chapters.length },
                    { label: "已有分镜", value: episodes ? `${withShots}/${episodeRows.length} 集` : episodesQuery.isError ? "没读到" : "…" },
                    { label: "检查提醒", value: lastOutcome ? lastOutcome.errors + lastOutcome.warnings : "—", tone: lastOutcome && lastOutcome.errors ? "bad" : lastOutcome?.warnings ? "warn" : undefined },
                ]}
            />
            <StageTabs<ScriptTab>
                tabs={[
                    { key: "episodes", label: "剧集", count: episodeRows.length || undefined },
                    { key: "source", label: "原文对照" },
                    { key: "check", label: "检查", count: checkItems.length || undefined, warn: Boolean(lastOutcome?.errors) },
                ]}
                active={active}
                onChange={onTab}
            />
            {!unit && unitsQuery.isError ? null : !unit ? (
                <div className="drama-flow-empty">
                    <strong>{stage?.state === "running" ? "正在把原文改写成剧本" : "还没有剧本"}</strong>
                    <span>{stage?.state === "running" ? "写好一集出现一集，不用守着；点右上角可以看流式输出。" : "点右上角「剧本优化」把原文改成剧集，或者直接贴剧本。"}</span>
                </div>
            ) : active === "episodes" ? (
                <div className="drama-flow-split">
                    <div className="drama-flow-cards" role="list" aria-label="剧集">
                        {rail.units.map((item, index) => {
                            const row = episodeRows.find((candidate) => candidate.unitId === item.id);
                            return (
                                <button key={item.id} type="button" role="listitem" className="drama-flow-card" aria-pressed={item.id === unit.id} onClick={() => onSelectUnit(item.id)}>
                                    <strong>{item.title || `第 ${index + 1} ${rail.noun}`}</strong>
                                    <span>
                                        {(row?.wordCount ?? item.wordCount).toLocaleString("zh-CN")} 字{row ? ` · 来源 ${row.sources.length} 处` : ""}
                                    </span>
                                    <span data-tone={row?.shots ? "ok" : undefined}>{row ? (row.shots ? `已分镜 ${row.shots} 个镜头` : row.hasShotlist ? "镜头表是草稿" : "还没分镜") : ""}</span>
                                </button>
                            );
                        })}
                    </div>
                    <EpisodeScript
                        key={unit.id}
                        projectId={project.id}
                        unit={unit}
                        full={fullUnit}
                        loadError={workspaceQuery.isError}
                        onReload={() => void workspaceQuery.refetch()}
                        episode={unitEpisodeNumber(allUnits, unit.id)}
                        hasShots={Boolean(episodeRows.find((row) => row.unitId === unit.id)?.shots)}
                        rule={dramaRule}
                        onRedoShots={() => onRedoShots(unit.id)}
                    />
                </div>
            ) : active === "source" ? (
                <SourceCompare
                    projectId={project.id}
                    unit={unit}
                    full={fullUnit}
                    loadError={workspaceQuery.isError}
                    onReload={() => void workspaceQuery.refetch()}
                    units={allUnits}
                    sources={episodeRows.find((row) => row.unitId === unit.id)?.sources || []}
                />
            ) : (
                <div className="drama-flow-panel">
                    <div className="drama-flow-panel-head">
                        <h3>最近一次剧本优化的检查</h3>
                        <span>{lastOutcome ? `${lastOutcome.episodes.length} 集 · 解析 ${Math.round(lastOutcome.parse.rate * 100)}% · ${lastOutcome.errors} 个错误 · ${lastOutcome.warnings} 条提醒` : "还没有剧本优化记录"}</span>
                    </div>
                    {checkItems.length ? (
                        <ul className="drama-flow-issues">
                            {checkItems.map((item, index) => (
                                <li key={`${item.rule}-${index}`} data-level={item.level}>
                                    <strong>{item.level === "error" ? "错误" : "提醒"}</strong>
                                    <span>{item.message}</span>
                                    <code>{item.rule}</code>
                                </li>
                            ))}
                        </ul>
                    ) : lastOutcome ? (
                        <p className="drama-flow-muted">检查没有问题。台词写法不对的行，生成分镜时会当成动作。</p>
                    ) : null}
                </div>
            )}
        </StageLayout>
    );
}

/**
 * 一集的场次和正文。unit 是列表里的摘要（不带正文），full 是 workspace 返回的完整单元：
 * 正文只从 full 读，没读到之前不显示正文、不能改，保存时在 full 的正文上保留来源注释。
 */
function EpisodeScript({
    projectId,
    unit,
    full,
    loadError,
    onReload,
    episode,
    hasShots,
    rule,
    onRedoShots,
}: {
    projectId: string;
    unit: ProjectUnit;
    full?: ProjectUnit;
    loadError: boolean;
    onReload: () => void;
    episode: number;
    hasShots: boolean;
    rule?: { maxSeconds: number; minSeconds: number };
    onRedoShots: () => void;
}) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [draft, setDraft] = useState<string | undefined>(undefined);
    const [savedWithShots, setSavedWithShots] = useState(false);
    const text = full ? scriptHtmlToText(full.sourceText || "") : undefined;
    const scenesQuery = useQuery({
        queryKey: [...dramaScenesKey(projectId, unit.id), unit.updatedAt, rule?.maxSeconds ?? 0, rule?.minSeconds ?? 0],
        queryFn: ({ signal }) => getDramaScenes(projectId, unit.id, signal, rule),
    });
    const save = useMutation({
        mutationFn: (next: string) => {
            if (!full) throw new Error("正文还没读到，不能保存");
            return updateProjectUnit(projectId, unit.id, { sourceText: withEpisodeSourcesComment(full.sourceText || "", plainTextToHtml(next)) });
        },
        onSuccess: async () => {
            await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId, "units"] }), queryClient.invalidateQueries({ queryKey: ["project", projectId, "unit-workspace", unit.id] })]);
            setDraft(undefined);
            setSavedWithShots(hasShots);
            message.success("剧本已保存");
        },
        onError: (error) => message.error(error instanceof Error ? `剧本没保存上，改动还在：${error.message}` : "剧本没保存上，改动还在"),
    });
    const scenes = scenesQuery.data?.scenes || [];
    const editing = draft !== undefined;
    return (
        <div className="drama-flow-panel">
            <div className="drama-flow-panel-head">
                <h3>{unit.title}</h3>
                <div className="drama-flow-work-actions">
                    <span>{text === undefined ? (loadError ? "正文没读到" : "正在读正文…") : `${text.length.toLocaleString("zh-CN")} 字`}</span>
                    {editing ? (
                        <>
                            <Button size="small" disabled={save.isPending} onClick={() => setDraft(undefined)}>
                                放弃改动
                            </Button>
                            <Button size="small" type="primary" loading={save.isPending} disabled={!draft.trim() || draft === text} onClick={() => save.mutate(draft)}>
                                保存
                            </Button>
                        </>
                    ) : (
                        <Button size="small" icon={<Pencil className="size-3.5" />} disabled={text === undefined} onClick={() => setDraft(text)}>
                            改剧本
                        </Button>
                    )}
                </div>
            </div>
            {savedWithShots ? (
                <div className="drama-flow-note" data-tone="warn">
                    这一集已经有分镜，分镜对应的是改动前的剧本。
                    <Button size="small" type="link" onClick={onRedoShots}>
                        去重做这一集分镜
                    </Button>
                </div>
            ) : null}
            <div>
                <div className="drama-flow-subhead">场次 {scenesQuery.data ? `· ${scenes.length} 场${scenesQuery.data.paletteReady ? " · 色卡已有" : " · 色卡还没做"}` : ""}</div>
                {scenesQuery.isError ? (
                    <ReadError what="场次表" onRetry={() => void scenesQuery.refetch()} />
                ) : scenes.length ? (
                    <table className="drama-flow-table">
                        <thead>
                            <tr>
                                <th>场</th>
                                <th>场次头</th>
                                <th>正文行</th>
                                <th>小节</th>
                                <th>色卡</th>
                            </tr>
                        </thead>
                        <tbody>
                            {scenes.map((scene) => (
                                <tr key={scene.id}>
                                    <td>{dramaSceneCode(episode, scene.no)}</td>
                                    <td>{scene.heading}</td>
                                    <td>
                                        {scene.lines[0]}–{scene.lines[1]}
                                    </td>
                                    <td>{scene.sections?.length ?? "—"}</td>
                                    <td>{scene.paletteAssetId ? "有" : "没有"}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                ) : (
                    <p className="drama-flow-muted">{scenesQuery.isLoading ? "正在读场次…" : "正文里还没认出场次头。"}</p>
                )}
            </div>
            {editing ? (
                <Input.TextArea aria-label="剧本正文" autoSize={{ minRows: 16, maxRows: 48 }} value={draft} onChange={(event) => setDraft(event.target.value)} />
            ) : text === undefined ? (
                loadError ? (
                    <ReadError what="这一集的正文" onRetry={onReload} />
                ) : (
                    <p className="drama-flow-muted">正在读正文…</p>
                )
            ) : text ? (
                <div className="drama-flow-script-text">{text}</div>
            ) : (
                <p className="drama-flow-muted">这一集还没有正文，点「改剧本」贴进来。</p>
            )}
        </div>
    );
}

/** 原文对照：剧集正文用 workspace 的完整单元，来源章节按 id 单独读完整正文（列表摘要不带正文）。 */
function SourceCompare({
    projectId,
    unit,
    full,
    loadError,
    onReload,
    units,
    sources,
}: {
    projectId: string;
    unit: ProjectUnit;
    full?: ProjectUnit;
    loadError: boolean;
    onReload: () => void;
    units: ProjectUnit[];
    sources: Array<{ unitId: string; from: number; to: number }>;
}) {
    const sourceIds = [...new Set(sources.map((source) => source.unitId))];
    const chapterQueries = useQueries({
        queries: sourceIds.map((id) => ({
            queryKey: ["project", projectId, "unit-full", id] as const,
            queryFn: async () => (await getProjectUnit(projectId, id)).unit,
            enabled: units.some((item) => item.id === id),
            staleTime: 60_000,
        })),
    });
    const text = full ? scriptHtmlToText(full.sourceText || "") : undefined;
    return (
        <div className="drama-flow-compare">
            <div className="drama-flow-panel">
                <div className="drama-flow-panel-head">
                    <h3>剧本 · {unit.title}</h3>
                </div>
                {text === undefined ? loadError ? <ReadError what="这一集的正文" onRetry={onReload} /> : <p className="drama-flow-muted">正在读正文…</p> : <div className="drama-flow-script-text">{text || "还没有正文"}</div>}
            </div>
            <div className="drama-flow-panel">
                <div className="drama-flow-panel-head">
                    <h3>原文</h3>
                    <span>{sources.length ? `${sources.length} 处来源` : "没有来源记录"}</span>
                </div>
                {sources.length ? (
                    sources.map((source) => {
                        const summary = units.find((item) => item.id === source.unitId);
                        const query = chapterQueries[sourceIds.indexOf(source.unitId)];
                        return (
                            <div key={`${source.unitId}-${source.from}`} className="drama-flow-source">
                                <div className="drama-flow-subhead">
                                    {summary?.title || "已删除的章节"} · 第 {source.from}–{source.to} 段
                                </div>
                                {!summary ? (
                                    <p className="drama-flow-muted">这一章已经不在项目里了。</p>
                                ) : query?.isError ? (
                                    <ReadError what="这一章的原文" onRetry={() => void query.refetch()} />
                                ) : query?.data ? (
                                    <div className="drama-flow-script-text">{sourceParagraphs(query.data.sourceText || "", source.from, source.to).join("\n")}</div>
                                ) : (
                                    <p className="drama-flow-muted">正在读原文…</p>
                                )}
                            </div>
                        );
                    })
                ) : (
                    <p className="drama-flow-muted">这一集是直接导入的剧本，或者是旧数据，没有记录来自哪一章。</p>
                )}
            </div>
        </div>
    );
}
