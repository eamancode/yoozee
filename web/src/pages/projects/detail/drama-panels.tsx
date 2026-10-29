import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Checkbox, Dropdown, Input, InputNumber, Space } from "antd";
import { CheckCircle2, ChevronDown, Circle, CircleAlert, Download, FileText, MoreHorizontal, Pause, Pencil, Play, ShieldCheck } from "lucide-react";
import { Link } from "react-router";

import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { Callout } from "@/components/ui/product/callout";
import { Select } from "@/components/ui/base/select";
import { SegmentedControl } from "@/components/ui/base/segmented-control";
import { compileProjectShot, createDramaEvidence, downloadDramaDelivery, dramaShotGenreLabel, getDramaBible, getDramaBrief, getDramaShotlist, getDramaShotVocab, putDramaBible, putDramaBrief, framesShotArtifact, getDramaProgress, listDramaEvidence, listDramaSegments, parseEvidenceMetrics, pickShotArtifact, runDramaGate, transcribeShotArtifact, writeDramaSegments, type DramaBible, type DramaEvidence, type DramaEvidenceKind, type DramaGateCheck, type DramaLine, type DramaProgress, type DramaSegment, type DramaSegmentProgress, type DramaSegmentView, type DramaShot, type DramaShotIssue, type DramaSubShot } from "@/services/api/drama";
import { listProjectAssets, type ShotArtifact } from "@/services/api/projects";
import { resourceFileUrl } from "@/services/api/resources";
import { waitForGenerationTask } from "@/services/api/task-center";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import type { ReferenceImage } from "@/types/image";

import { dramaAuthLabel } from "../drama-model";
import { buildAssetChecklist, checklistSummary, draftBibleFromAssets, setChecklistChecked, type ChecklistItem, type ChecklistKind } from "./drama-asset-checklist";
import { formatDialogue, parseDialogue, type SegmentDurationRule } from "./chapter-segments";
import { editShotCard, editSubShot, failedSegmentEditHint, mergeWithNext, moveEdgeShot, segmentLacksReferences, shotFixEdit, splitSegment, subShotSeconds, type SegmentEdit } from "./segment-edit";
import { ShotCardEditor } from "./shot-card-editor";
import { changeChapterSectionGenre } from "./project-chapter-ai";
import { sectionGenreConfirm, sectionGenreSpan, sectionGenreTarget, type SectionGenreTarget } from "./section-genre";
import { issueFixLabel, issueFixShots, scriptLineOptions, shotIssues } from "./shot-issues";
import { cardBoundAssets, sectionLayout, sectionLayoutBadge, sectionOutputFor, type SegmentAsset } from "./shot-card-layout";
import { GenreShotCard, SectionLayoutHead, SectionLayoutTail } from "./genre-shot-cards";
import { projectSceneCode } from "./unit-numbering";
import { StyleBibleEditor } from "./drama-style-bible-editor";
import { EVIDENCE_LABELS, issueSegments, pendingReviewSegments, reviewGap } from "./drama-review-queue";
import { applyBriefFacts, bibleSummary, BRIEF_FACTS, BRIEF_STATUSES, briefConfirmedCount, briefFactsFromDocument, dramaBriefKey, parseDocumentJson, type BriefFactDraft, type BriefFactKey } from "./drama-brief";

export const dramaProgressKey = (projectId: string) => ["project", projectId, "drama", "progress"] as const;
export const dramaEvidenceKey = (projectId: string) => ["project", projectId, "drama", "evidence"] as const;
export const dramaSegmentsKey = (projectId: string, unitId: string) => ["project", projectId, "drama", "segments", unitId] as const;
/** 一章的镜头表（与章节页「生成分段」侧栏同一个 key）。 */
export const dramaShotlistKey = (projectId: string, unitId: string) => ["project", projectId, "drama-shotlist", unitId] as const;

export function useDramaProgress(projectId: string) {
    return useQuery({ queryKey: dramaProgressKey(projectId), queryFn: async ({ signal }) => (await getDramaProgress(projectId, signal)).progress });
}

/** 刷新进度与证据：生成、过审、选中、交付之后都要调用。 */
export function useInvalidateDrama(projectId: string) {
    const queryClient = useQueryClient();
    return () => Promise.all([
        queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) }),
        queryClient.invalidateQueries({ queryKey: dramaEvidenceKey(projectId) }),
    ]);
}

const SEGMENT_CELLS: Array<{ key: keyof Pick<DramaSegmentProgress, "script" | "refs" | "generated" | "reviewed" | "inCut" | "accepted">; label: string }> = [
    { key: "script", label: "剧本" },
    { key: "refs", label: "参考" },
    { key: "generated", label: "生成" },
    { key: "reviewed", label: "过审" },
    { key: "inCut", label: "入片" },
    { key: "accepted", label: "验收" },
];


/** 项目概览里的「终态」面板：一句进度、十个可验证终态和每段六格证据。 */
export function DramaOverviewPanel({ projectId }: { projectId: string }) {
    const query = useDramaProgress(projectId);
    const [expanded, setExpanded] = useState(false);
    const [documentOpen, setDocumentOpen] = useState<"brief" | "bible" | null>(null);
    const [bibleView, setBibleView] = useState<"style" | "json">("json");
    const progress = query.data;
    if (query.isLoading) return <section className="rounded-xl border border-border/70 bg-surface p-5 text-xs text-foreground/45">正在读取段级进度…</section>;
    if (query.isError || !progress) return <section className="rounded-xl border border-border/70 bg-surface p-5 text-xs text-foreground/55">段级进度读取失败：{query.error instanceof Error ? query.error.message : "未知错误"} <Button size="small" type="link" onClick={() => void query.refetch()}>重试</Button></section>;
    const segments = expanded ? progress.segments : progress.segments.slice(0, 12);
    return (
        <section className="rounded-xl border border-border/70 bg-surface p-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="text-[var(--fs-micro)] font-medium uppercase tracking-[.18em] text-[var(--workspace-accent)]">yoozee 1.0 · 段级进度</div>
                    <h2 className="mt-1.5 text-base font-semibold">{progress.line || "还没有分段"}</h2>
                    {progress.next ? <p className="mt-1 text-xs leading-5 text-foreground/50">下一步：{progress.next.label} · {progress.next.detail}（{progress.next.skill}）</p> : <p className="mt-1 text-xs text-foreground/50">{progress.endStates.length} 个终态全部达成。</p>}
                </div>
                <DramaModelChips progress={progress} />
            </div>
            <ol className="mt-4 grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
                {progress.endStates.map((state, index) => (
                    <li key={state.key} className={`rounded-lg border px-3 py-2.5 ${state.done ? "border-border/50 bg-surface-active" : progress.next?.key === state.key ? "border-[var(--workspace-accent)]/50" : "border-border/70"}`}>
                        <div className="flex items-center gap-1.5 text-xs font-medium">{state.done ? <CheckCircle2 className="size-3.5 text-[var(--workspace-accent)]" /> : <Circle className="size-3.5 text-foreground/30" />}<span className="text-foreground/40">{index + 1}</span><span className="truncate">{state.label}</span></div>
                        <p className="mt-1 line-clamp-2 text-[var(--fs-label)] leading-5 text-foreground/50">{state.detail}</p>
                        <span className="mt-1 flex items-center justify-between gap-2 text-[var(--fs-tiny)] text-foreground/35"><span>{state.skill}</span>{DOCUMENT_BY_END_STATE[state.key] ? <Button type="link" size="small" className="!h-auto !px-0 !text-[var(--fs-tiny)]" icon={<FileText className="size-3" />} onClick={() => { setBibleView(state.key === "style" ? "style" : "json"); setDocumentOpen(DOCUMENT_BY_END_STATE[state.key]!); }}>{DOCUMENT_BY_END_STATE[state.key] === "brief" ? "打开需求文档" : state.key === "style" ? "看风格锚" : "打开连戏表"}</Button> : state.key === "palette" ? <Link to={`/projects/${projectId}/assets?category=palette`} className="text-[var(--fs-tiny)] text-[var(--workspace-accent)]">看色卡</Link> : null}</span>
                    </li>
                ))}
            </ol>
            <DramaBriefModal projectId={projectId} open={documentOpen === "brief"} onClose={() => setDocumentOpen(null)} />
            <DramaBibleModal projectId={projectId} open={documentOpen === "bible"} initialView={bibleView} onClose={() => setDocumentOpen(null)} />
            {progress.segments.length ? (
                <div className="mt-4 overflow-x-auto">
                    <table className="w-full min-w-[640px] text-left text-xs">
                        <thead className="text-[var(--fs-tiny)] text-foreground/40"><tr><th className="py-1.5 pr-2 font-medium">段</th><th className="py-1.5 pr-2 font-medium">时长</th>{SEGMENT_CELLS.map((cell) => <th key={cell.key} className="py-1.5 pr-2 text-center font-medium">{cell.label}</th>)}<th className="py-1.5 font-medium">候选 / 缺口</th></tr></thead>
                        <tbody>
                            {segments.map((segment) => (
                                <tr key={segment.shotId} className="border-t border-border/50">
                                    <td className="max-w-56 py-1.5 pr-2"><span className="font-medium">{segment.segmentId}</span> <span className="text-foreground/50">{segment.unitTitle ? `${segment.unitTitle} · ` : ""}{segment.title}</span></td>
                                    <td className={`py-1.5 pr-2 tabular-nums ${segment.overLimit ? "text-amber-600 dark:text-amber-400" : "text-foreground/60"}`}>{(segment.durationMs / 1000).toFixed(1)}s{segment.overLimit ? " 超长" : ""}</td>
                                    {SEGMENT_CELLS.map((cell) => <td key={cell.key} className="py-1.5 pr-2 text-center">{segment[cell.key] ? <CheckCircle2 className="inline size-3.5 text-[var(--workspace-accent)]" aria-label="已完成" /> : <Circle className="inline size-3.5 text-foreground/25" aria-label="未完成" />}</td>)}
                                    <td className="py-1.5 text-foreground/50">{segment.takes} 条{segment.missingEvidence?.length ? ` · 缺${segment.missingEvidence.map((kind) => EVIDENCE_LABELS[kind] || kind).join("、")}` : ""}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                    {progress.segments.length > 12 ? <Button className="mt-2" size="small" type="link" onClick={() => setExpanded((value) => !value)}>{expanded ? "收起" : `展开全部 ${progress.segments.length} 段`}</Button> : null}
                </div>
            ) : null}
            <DramaIssueQueue projectId={projectId} segments={progress.segments} />
            <DramaReviewQueue projectId={projectId} segments={progress.segments} />
        </section>
    );
}

/** 总览的带问题段列表：带着检查问题装段、还没改完的段，点了跳到制作台这一段逐张改（视频阶段，段时间轴在那里）。 */
function DramaIssueQueue({ projectId, segments }: { projectId: string; segments: DramaSegmentProgress[] }) {
    const marked = issueSegments(segments);
    if (!marked.length) return null;
    return (
        <div className="mt-4 border-t border-border/50 pt-3">
            <h3 className="text-xs font-medium">带问题的段 {marked.length}<span className="ml-1.5 font-normal text-foreground/45">先装段、还没改完的镜头表检查问题，按章节顺序</span></h3>
            <ul className="mt-2 grid gap-1.5 sm:grid-cols-2">
                {marked.map((segment) => (
                    <li key={segment.shotId}>
                        <Link to={`/projects/${projectId}/workflow/${segment.unitId}/video?shot=${encodeURIComponent(segment.shotId)}`} className="flex items-center gap-2 rounded-md border border-border/60 px-2.5 py-1.5 text-xs hover:border-[var(--workspace-accent)]/50">
                            <span className="min-w-0 flex-1 truncate"><span className="text-foreground/50">{segment.unitTitle || "未命名章节"} · </span><span className="font-medium">{segment.segmentId}</span> <span className="text-foreground/60">{segment.title.startsWith(segment.segmentId) ? segment.title.slice(segment.segmentId.length).trim() : segment.title}</span></span>
                            <span className="shrink-0 text-[var(--fs-label)] text-red-600 dark:text-red-400">{segment.issues} 个问题</span>
                        </Link>
                    </li>
                ))}
            </ul>
        </div>
    );
}

/** 总览的跨章节待审列表：章节 / 段 / 缺什么，点了跳到制作台的这一段（视频阶段）。不建审片表。 */
function DramaReviewQueue({ projectId, segments }: { projectId: string; segments: DramaSegmentProgress[] }) {
    const pending = pendingReviewSegments(segments);
    if (!pending.length) return null;
    return (
        <div className="mt-4 border-t border-border/50 pt-3">
            <h3 className="text-xs font-medium">待审 {pending.length} 段<span className="ml-1.5 font-normal text-foreground/45">已生成、还没过审，按章节顺序</span></h3>
            <ul className="mt-2 grid gap-1.5 sm:grid-cols-2">
                {pending.map((segment) => (
                    <li key={segment.shotId}>
                        <Link to={`/projects/${projectId}/workflow/${segment.unitId}/video?shot=${encodeURIComponent(segment.shotId)}`} className="flex items-center gap-2 rounded-md border border-border/60 px-2.5 py-1.5 text-xs hover:border-[var(--workspace-accent)]/50">
                            <span className="min-w-0 flex-1 truncate"><span className="text-foreground/50">{segment.unitTitle || "未命名章节"} · </span><span className="font-medium">{segment.segmentId}</span> <span className="text-foreground/60">{segment.title.startsWith(segment.segmentId) ? segment.title.slice(segment.segmentId.length).trim() : segment.title}</span></span>
                            <span className="shrink-0 text-[var(--fs-label)] text-amber-600 dark:text-amber-400">{reviewGap(segment)}</span>
                        </Link>
                    </li>
                ))}
            </ul>
        </div>
    );
}

// 「需求问透了」靠需求文档六项确认；「风格定了」看连戏表的风格锚；「参考齐了」靠连戏表有人物并逐项校验（「剧本定了」看剧集，在章节页做；
// 「色卡齐了」去资产页的色卡）。前端要能直接打开它们，不能只靠 Agent 写。
const DOCUMENT_BY_END_STATE: Record<string, "brief" | "bible" | undefined> = { brief: "brief", style: "bible", refs: "bible" };

/** 需求文档：六项事实可以逐项填写、改状态；其余字段（要求、不做、待确认……）在 JSON 里改。 */
export function DramaBriefModal({ projectId, open, onClose }: { projectId: string; open: boolean; onClose: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const briefQuery = useQuery({ queryKey: dramaBriefKey(projectId), queryFn: async () => (await getDramaBrief(projectId)).document, enabled: open });
    const [mode, setMode] = useState<"form" | "json">("form");
    const [title, setTitle] = useState("");
    const [facts, setFacts] = useState<Record<BriefFactKey, BriefFactDraft> | null>(null);
    const [jsonText, setJsonText] = useState("");
    useEffect(() => {
        if (!open || !briefQuery.data) return;
        setMode("form");
        setTitle(typeof briefQuery.data.title === "string" ? briefQuery.data.title : "");
        setFacts(briefFactsFromDocument(briefQuery.data));
        setJsonText(JSON.stringify(briefQuery.data, null, 2));
    }, [briefQuery.data, open]);
    const formDocument = () => ({ ...applyBriefFacts(briefQuery.data || {}, facts || briefFactsFromDocument({})), ...(title.trim() ? { title: title.trim() } : {}) });
    const switchMode = (next: "form" | "json") => {
        if (next === mode) return;
        try {
            if (next === "json") setJsonText(JSON.stringify(formDocument(), null, 2));
            else {
                const parsed = parseDocumentJson(jsonText);
                setTitle(typeof parsed.title === "string" ? parsed.title : "");
                setFacts(briefFactsFromDocument(parsed));
            }
            setMode(next);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "JSON 格式不对");
        }
    };
    const save = useMutation({
        mutationFn: () => putDramaBrief(projectId, mode === "json" ? parseDocumentJson(jsonText) : formDocument()),
        onSuccess: ({ document }) => {
            queryClient.setQueryData(dramaBriefKey(projectId), document);
            // 需求文档里的生成模型、授权档位会同步到项目字段，项目和进度都要刷新。
            void queryClient.invalidateQueries({ queryKey: ["project", projectId] });
            message.success("需求文档已保存");
            onClose();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "需求文档没保存上"),
    });
    const confirmed = facts ? briefConfirmedCount(facts) : 0;
    return (
        <AppModal open={open} title="需求文档" width="min(720px, calc(100vw - 32px))" okText="保存需求文档" cancelText="取消" okButtonProps={{ loading: save.isPending, disabled: !briefQuery.data }} onOk={() => save.mutate()} onCancel={onClose}>
            {briefQuery.isLoading ? <p className="py-6 text-center text-xs text-foreground/45">正在读取需求文档…</p> : briefQuery.isError ? <p className="py-6 text-center text-xs text-foreground/55">需求文档读取失败：{briefQuery.error instanceof Error ? briefQuery.error.message : "未知错误"}</p> : (
                <div className="grid gap-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <p className="m-0 text-[var(--fs-label)] text-foreground/50">六项都标成「已确认」或「默认，已授权」，终态「需求问透了」才成立。当前 {confirmed}/6。</p>
                        <SegmentedControl size="sm" value={mode} onChange={(value) => switchMode(value as "form" | "json")} options={[{ value: "form", label: "六项事实" }, { value: "json", label: "完整 JSON" }]} />
                    </div>
                    {mode === "form" && facts ? (
                        <div className="grid gap-3">
                            <label className="grid gap-1 text-xs"><span className="font-medium text-foreground/62">片名</span><Input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="项目或这一集的片名" /></label>
                            {BRIEF_FACTS.map((fact) => (
                                <div key={fact.key} className="grid gap-1 text-xs">
                                    <span className="font-medium text-foreground/62">{fact.label}</span>
                                    <div className="flex flex-col gap-2 sm:flex-row">
                                        <Input.TextArea className="min-w-0 flex-1" autoSize={{ minRows: 1, maxRows: 4 }} value={facts[fact.key].value} placeholder={fact.placeholder} aria-label={fact.label} onChange={(event) => setFacts((current) => current ? { ...current, [fact.key]: { ...current[fact.key], value: event.target.value } } : current)} />
                                        <Select className="sm:!w-40" value={facts[fact.key].status} options={BRIEF_STATUSES.map((status) => ({ value: status, label: status }))} ariaLabel={`${fact.label}状态`} onChange={(status: string) => setFacts((current) => current ? { ...current, [fact.key]: { ...current[fact.key], status } } : current)} />
                                    </div>
                                </div>
                            ))}
                            <p className="m-0 text-[var(--fs-tiny)] leading-5 text-foreground/40">值留空的一项不写入。要求（R-）、不做、约束、待确认问题请在「完整 JSON」里改，或用看片反馈追加。</p>
                        </div>
                    ) : (
                        <Input.TextArea className="font-mono !text-xs" autoSize={{ minRows: 14, maxRows: 26 }} value={jsonText} onChange={(event) => setJsonText(event.target.value)} aria-label="需求文档 JSON" spellCheck={false} />
                    )}
                </div>
            )}
        </AppModal>
    );
}

/** 连戏表：看人物、道具、场景的数量和未校验项；没人物时一键按已确认资产起草，细项在 JSON 里改，逐项过门禁去资产页。 */
export function DramaBibleModal({ projectId, open, initialView = "json", onClose }: { projectId: string; open: boolean; initialView?: "style" | "json"; onClose: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const bibleQuery = useQuery({ queryKey: dramaBibleKey(projectId), queryFn: async () => (await getDramaBible(projectId)).document, enabled: open });
    const assetsQuery = useQuery({ queryKey: dramaChecklistAssetsKey(projectId), queryFn: async () => (await listProjectAssets(projectId)).assets, enabled: open });
    const [jsonText, setJsonText] = useState("");
    const [view, setView] = useState<"style" | "json">(initialView);
    useEffect(() => {
        if (open) setView(initialView);
    }, [initialView, open]);
    useEffect(() => {
        if (open && bibleQuery.data) setJsonText(JSON.stringify(bibleQuery.data, null, 2));
    }, [bibleQuery.data, open]);
    // 风格锚、色彩脚本的编辑直接改 JSON 草稿，「保存连戏表」一起提交；JSON 写坏时先回完整 JSON 改好。
    const draft = useMemo(() => {
        try {
            return parseDocumentJson(jsonText) as DramaBible;
        } catch {
            return undefined;
        }
    }, [jsonText]);
    const save = useMutation({
        mutationFn: (document: DramaBible) => putDramaBible(projectId, document),
        onSuccess: ({ document }) => {
            queryClient.setQueryData(dramaBibleKey(projectId), document);
            setJsonText(JSON.stringify(document, null, 2));
            void queryClient.invalidateQueries({ queryKey: ["project", projectId, "drama"] });
            message.success("连戏表已保存");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "连戏表没保存上"),
    });
    const summary = bibleSummary(bibleQuery.data);
    const assets = assetsQuery.data || [];
    const draftable = assets.some((asset) => asset.category === "character" || asset.category === "environment" || asset.category === "prop");
    const saveJson = () => {
        try {
            save.mutate(parseDocumentJson(jsonText) as DramaBible);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "JSON 格式不对");
        }
    };
    return (
        <AppModal open={open} title="连戏表" width="min(760px, calc(100vw - 32px))" okText="保存连戏表" cancelText="关闭" okButtonProps={{ loading: save.isPending, disabled: !bibleQuery.data }} onOk={saveJson} onCancel={onClose}>
            {bibleQuery.isLoading ? <p className="py-6 text-center text-xs text-foreground/45">正在读取连戏表…</p> : bibleQuery.isError ? <p className="py-6 text-center text-xs text-foreground/55">连戏表读取失败：{bibleQuery.error instanceof Error ? bibleQuery.error.message : "未知错误"}</p> : (
                <div className="grid gap-3">
                    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-surface-active px-3 py-2 text-xs">
                        <span className="text-foreground/60">人物 {summary.characters} · 道具 {summary.props} · 场景 {summary.locations}{summary.unchecked ? ` · ${summary.unchecked} 项没过门禁` : ""}</span>
                        <span className="flex flex-wrap items-center gap-2">
                            {summary.characters === 0 ? <Button size="small" disabled={!draftable} loading={save.isPending} onClick={() => save.mutate(draftBibleFromAssets(bibleQuery.data, assets))}>按已确认资产起草</Button> : null}
                            <Link to={`/projects/${projectId}/assets`} onClick={onClose} className="text-[var(--workspace-accent)]">去资产页逐项过门禁</Link>
                        </span>
                    </div>
                    <p className="m-0 text-[var(--fs-label)] leading-5 text-foreground/50">「风格定了」要求有风格锚；「参考齐了」要求每个造型、道具、场景都勾过门禁。characters、props、locations、color_script 等字段必须是数组。</p>
                    <SegmentedControl size="sm" value={view} onChange={(value) => setView(value as "style" | "json")} options={[{ value: "style", label: "风格锚与色彩脚本" }, { value: "json", label: "完整 JSON" }]} />
                    {view === "style" ? (
                        draft ? <StyleBibleEditor document={draft} onChange={(next) => setJsonText(JSON.stringify(next, null, 2))} /> : <Callout tone="warning" title="连戏表 JSON 写坏了">先切到「完整 JSON」改好格式。</Callout>
                    ) : (
                        <Input.TextArea className="font-mono !text-xs" autoSize={{ minRows: 14, maxRows: 26 }} value={jsonText} onChange={(event) => setJsonText(event.target.value)} aria-label="连戏表 JSON" spellCheck={false} />
                    )}
                </div>
            )}
        </AppModal>
    );
}

export function DramaModelChips({ progress }: { progress: DramaProgress }) {
    return (
        <div className="flex flex-wrap gap-1.5 text-[var(--fs-label)]">
            <span className="rounded bg-foreground/10 px-2 py-0.5 text-foreground/65">{progress.model.label} · 每段最长 {progress.model.duration.max} 秒</span>
            <span className="rounded bg-foreground/10 px-2 py-0.5 text-foreground/65">{dramaAuthLabel(progress.authMode)}</span>
            {!progress.model.calibrated ? <span className="rounded bg-amber-500/10 px-2 py-0.5 text-amber-600 dark:text-amber-400">档案未校准</span> : null}
            {progress.counts.overLimit ? <span className="rounded bg-amber-500/10 px-2 py-0.5 text-amber-600 dark:text-amber-400">{progress.counts.overLimit} 段超长</span> : null}
        </div>
    );
}

/** 交付阶段：成片门禁（同步、黑场/静音、响度、真峰值）和交付包下载。 */
export function DramaDeliveryPanel({ projectId, projectName }: { projectId: string; projectName: string }) {
    const { message } = App.useApp();
    const invalidate = useInvalidateDrama(projectId);
    const progressQuery = useDramaProgress(projectId);
    const evidenceQuery = useQuery({ queryKey: dramaEvidenceKey(projectId), queryFn: async ({ signal }) => (await listDramaEvidence(projectId, signal)).evidence });
    const lastGate = useMemo(() => (evidenceQuery.data || []).filter((item) => item.kind === "gate").sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0], [evidenceQuery.data]);
    const gateChecks = useMemo(() => {
        if (!lastGate) return [] as DramaGateCheck[];
        const checks = parseEvidenceMetrics(lastGate).checks;
        return Array.isArray(checks) ? (checks as DramaGateCheck[]) : [];
    }, [lastGate]);
    const gateMutation = useMutation({
        mutationFn: () => runDramaGate(projectId, {}),
        onSuccess: ({ gate }) => { void invalidate(); message[gate.report.passed ? "success" : "warning"](gate.report.passed ? "成片门禁通过" : "成片门禁未通过，看下面的明细"); },
        onError: (error) => message.error(error instanceof Error ? error.message : "门禁检查失败"),
    });
    const deliveryMutation = useMutation({
        mutationFn: () => downloadDramaDelivery(projectId, `${projectName}-交付包.zip`),
        onSuccess: (fileName) => { void invalidate(); message.success(`已下载 ${fileName}`); },
        onError: (error) => message.error(error instanceof Error ? error.message : "交付包生成失败"),
    });
    return (
        <div className="mt-5 rounded-xl border border-border/70 bg-surface p-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h3 className="text-sm font-semibold">成片门禁与交付包</h3>
                    <p className="mt-1 text-xs leading-5 text-foreground/48">门禁检查最近一次剪辑导出的成片：音画同步、黑场与静音、响度（目标 ±1 LUFS）和真峰值。交付包含需求文档、连戏表、分段表、生成参数、资产清单、证据、进度、验收单和字幕；不含任何密钥。</p>
                </div>
                {progressQuery.data ? <DramaModelChips progress={progressQuery.data} /> : null}
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
                <Button icon={<ShieldCheck className="size-3.5" />} loading={gateMutation.isPending} onClick={() => gateMutation.mutate()}>{lastGate ? "重新检查成片" : "检查成片"}</Button>
                <Button type="primary" icon={<Download className="size-3.5" />} loading={deliveryMutation.isPending} onClick={() => deliveryMutation.mutate()}>下载交付包</Button>
            </div>
            <DramaFeedbackForm projectId={projectId} segments={progressQuery.data?.segments || []} />
            {lastGate ? (
                <div className="mt-4">
                    <div className="flex items-center gap-1.5 text-xs font-medium">{lastGate.passed ? <CheckCircle2 className="size-3.5 text-[var(--workspace-accent)]" /> : <CircleAlert className="size-3.5 text-amber-500" />}<span>{lastGate.passed ? "最近一次门禁通过" : "最近一次门禁未通过"}</span><span className="font-normal text-foreground/40">{new Date(lastGate.createdAt).toLocaleString("zh-CN")}</span></div>
                    <ul className="mt-2 grid gap-1 text-[var(--fs-label)] leading-5 sm:grid-cols-2">
                        {gateChecks.map((check) => <li key={check.name} className={check.ok ? "text-foreground/55" : "text-amber-600 dark:text-amber-400"}>{check.ok ? "✓" : "✗"} {check.name}：{check.detail}</li>)}
                    </ul>
                </div>
            ) : null}
        </div>
    );
}

function latestOf(items: DramaEvidence[], kind: DramaEvidenceKind) {
    return items.filter((item) => item.kind === kind).sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0];
}

/**
 * 一条视频候选的证据面板：抽帧、人脸（人判）、台词转写（有台词才要），三项都过才能选定。
 * 判人脸、看抽帧必须由人在这里点；Agent 看不到画面时不替人判。
 */
export function DramaTakeEvidencePanel({ projectId, shotId, artifact, characterImages, onPicked }: { projectId: string; shotId: string; artifact: ShotArtifact; characterImages: ReferenceImage[]; onPicked: () => Promise<unknown> | void }) {
    const { message } = App.useApp();
    const invalidate = useInvalidateDrama(projectId);
    const progressQuery = useDramaProgress(projectId);
    const evidenceQuery = useQuery({ queryKey: dramaEvidenceKey(projectId), queryFn: async ({ signal }) => (await listDramaEvidence(projectId, signal)).evidence });
    const [reason, setReason] = useState("");
    const segment = progressQuery.data?.segments.find((item) => item.shotId === shotId);
    const hasDialogue = Boolean(segment?.hasDialogue);
    const items = useMemo(() => (evidenceQuery.data || []).filter((item) => item.artifactId === artifact.id), [artifact.id, evidenceQuery.data]);
    const frames = latestOf(items, "frames");
    const contactSheet = items.filter((item) => item.kind === "frames" && item.resourceId).sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0];
    const faces = latestOf(items, "faces");
    const transcript = latestOf(items, "transcript");
    const required: Array<{ kind: DramaEvidenceKind; label: string; evidence?: DramaEvidence }> = [
        { kind: "frames", label: "抽帧", evidence: frames },
        { kind: "faces", label: "人脸", evidence: faces },
        ...(hasDialogue ? [{ kind: "transcript" as const, label: "台词", evidence: transcript }] : []),
    ];
    const missing = required.filter((item) => item.evidence?.passed !== true);
    const onError = (fallback: string) => (error: unknown) => message.error(error instanceof Error ? error.message : fallback);
    const framesMutation = useMutation({ mutationFn: () => framesShotArtifact(projectId, artifact.id), onSuccess: () => void invalidate(), onError: onError("抽帧失败") });
    const markMutation = useMutation({
        mutationFn: (input: { kind: DramaEvidenceKind; passed: boolean; note: string }) => createDramaEvidence(projectId, { artifactId: artifact.id, ...input }),
        onSuccess: () => void invalidate(),
        onError: onError("证据保存失败"),
    });
    const transcribeMutation = useMutation({
        mutationFn: async () => {
            const { task } = await transcribeShotArtifact(projectId, artifact.id);
            await waitForGenerationTask(task.id, { initialTask: task });
            return createDramaEvidence(projectId, { artifactId: artifact.id, kind: "transcript", taskId: task.id });
        },
        onSuccess: ({ evidence }) => { void invalidate(); const metrics = parseEvidenceMetrics(evidence); message[evidence.passed ? "success" : "warning"](`台词字错率 ${typeof metrics.cer === "number" ? `${(metrics.cer * 100).toFixed(1)}%` : "—"}${evidence.passed ? "，通过" : "，未通过"}`); },
        onError: onError("转写失败"),
    });
    const pickMutation = useMutation({
        mutationFn: () => pickShotArtifact(projectId, artifact.id, reason.trim()),
        onSuccess: async () => { setReason(""); await invalidate(); await onPicked(); message.success("已选定这条候选"); },
        onError: onError("选定失败"),
    });
    const transcriptMetrics = transcript ? parseEvidenceMetrics(transcript) : {};
    return (
        <div className="mt-3 rounded-lg border border-border/70 p-3 text-xs">
            <div className="flex items-center justify-between gap-2"><span className="font-medium">候选证据 · v{artifact.version}</span>{artifact.selected ? <span className="rounded bg-foreground/10 px-1.5 py-0.5 text-[var(--fs-tiny)] text-foreground/60">已选定</span> : null}</div>
            <div className="mt-2 space-y-2">
                <EvidenceRow label="抽帧" evidence={frames} pending={frames && frames.passed === null ? "待人看" : undefined}>
                    <Button size="small" loading={framesMutation.isPending} onClick={() => framesMutation.mutate()}>{contactSheet ? "重新抽帧" : "出抽帧拼图"}</Button>
                    {contactSheet ? <><Button size="small" onClick={() => markMutation.mutate({ kind: "frames", passed: true, note: "人看过抽帧：无穿帮、无多余肢体和文字" })}>画面没问题</Button><Button size="small" danger onClick={() => markMutation.mutate({ kind: "frames", passed: false, note: "人看过抽帧：有问题" })}>有问题</Button></> : null}
                </EvidenceRow>
                {contactSheet?.resourceId ? <img className="w-full rounded-md bg-foreground/5" src={resourceFileUrl(contactSheet.resourceId)} alt="候选抽帧拼图" /> : null}
                <EvidenceRow label="人脸" evidence={faces}>
                    <Button size="small" disabled={!contactSheet} onClick={() => markMutation.mutate({ kind: "faces", passed: true, note: "人对照定妆照：同一个人" })}>同一个人</Button>
                    <Button size="small" danger disabled={!contactSheet} onClick={() => markMutation.mutate({ kind: "faces", passed: false, note: "人对照定妆照：不是同一个人" })}>不是</Button>
                </EvidenceRow>
                {contactSheet && characterImages.length ? <div className="flex gap-1.5 overflow-x-auto">{characterImages.map((image) => image.url ? <img key={image.id} className="h-16 shrink-0 rounded bg-foreground/5 object-cover" src={image.url} alt={`${image.name}参考`} title={image.name} /> : null)}</div> : null}
                {hasDialogue ? (
                    <EvidenceRow label="台词" evidence={transcript} detail={typeof transcriptMetrics.cer === "number" ? `字错率 ${((transcriptMetrics.cer as number) * 100).toFixed(1)}%` : undefined}>
                        <Button size="small" loading={transcribeMutation.isPending} onClick={() => transcribeMutation.mutate()}>{transcript ? "重新转写" : "转写并核对"}</Button>
                    </EvidenceRow>
                ) : null}
            </div>
            <div className="mt-3 flex gap-2">
                <Input size="small" value={reason} onChange={(event) => setReason(event.target.value)} placeholder="选它的理由（一句话）" maxLength={200} />
                <Button size="small" type="primary" disabled={Boolean(missing.length) || !reason.trim() || artifact.status !== "ready"} loading={pickMutation.isPending} onClick={() => pickMutation.mutate()}>选定</Button>
            </div>
            {missing.length ? <p className="mt-1.5 text-[var(--fs-tiny)] text-foreground/45">还缺：{missing.map((item) => item.label).join("、")}（三项都过才能选定）</p> : null}
        </div>
    );
}

function EvidenceRow({ label, evidence, pending, detail, children }: { label: string; evidence?: DramaEvidence; pending?: string; detail?: string; children: ReactNode }) {
    const state = pending || (evidence?.passed === true ? "通过" : evidence?.passed === false ? "未通过" : "未做");
    const tone = evidence?.passed === true ? "text-[var(--workspace-accent)]" : evidence?.passed === false ? "text-amber-600 dark:text-amber-400" : "text-foreground/40";
    return <div className="flex flex-wrap items-center gap-1.5"><span className="w-8 shrink-0 font-medium text-foreground/60">{label}</span><span className={`w-12 shrink-0 ${tone}`}>{state}</span>{detail ? <span className="text-foreground/45">{detail}</span> : null}<span className="ml-auto flex flex-wrap gap-1">{children}</span></div>;
}

/**
 * 分镜制作页的段卡：段内镜头按小节的戏类排成技能原本的分镜卡（卡 G：文戏 15 / 文戏 30 / 武戏版式，旧数据按统一镜头表），
 * 每张卡下列这一镜绑的角色子卡、道具、场景和本场色卡；以及「生成前看一眼」——按项目生成模型编译出的提示词、参考清单和检查结果。
 * 卡上可以拆段、与相邻段合并、把首尾镜头移到相邻段、逐张改分镜卡，保存走 PUT /drama/segments（改的是统一镜头表，保存后重检）。
 * 编译只读、不花钱；估价沿用工作台底部按渠道报出的积分。段没有人物/场景参考时明确提示，并给「选择资产」入口。
 * 编号一律按项目编号显示（第 2 集写 E02S01-P1），存下的数据仍是单元内编号。
 */
export function DramaSegmentCard({ projectId, unitId, shotId, revisionId, specJson, estimate, durationRule, genreRule, takeCounts, busyShotIds, episode = 0, boundAssets = [], onEdited, onPickAssets }: {
    projectId: string;
    unitId: string;
    shotId: string;
    revisionId?: string;
    specJson: string;
    estimate?: string;
    /** 段长规则（项目生成模型 + 当前视频渠道）；拆合、改时长后按它取合法段长。没有时只能看。 */
    durationRule?: SegmentDurationRule;
    /** 改戏类用的段长规则：和章节页生成分段同一个（项目默认视频模型的渠道段长），后端按它选默认戏类、切小节。没有时不能改戏类。 */
    genreRule?: { maxSeconds: number; minSeconds: number };
    /** 各段已有的生成结果数：合段会删掉被并入的段和它的结果，要先确认。 */
    takeCounts?: Record<string, number>;
    /** 正在生成的段：不能拆合。 */
    busyShotIds?: ReadonlySet<string>;
    /** 本单元在项目场次表里的集号：段、小节、镜头号按它显示成项目编号（0 / 1 原样）。 */
    episode?: number;
    /** 这一段绑的资产（角色子卡、道具、场景、本场色卡），按卡列在每张分镜卡下面。 */
    boundAssets?: SegmentAsset[];
    /** 保存成功后刷新；selectShotId 是保存后应选中的段（当前段并入上一段时）。 */
    onEdited?: (selectShotId?: string) => Promise<unknown> | void;
    /** 打开资产选择：选中的资产绑到这一段并写进段的参考。 */
    onPickAssets?: () => void;
}) {
    const { message, modal } = App.useApp();
    const queryClient = useQueryClient();
    const [open, setOpen] = useState(false);
    const [editingShot, setEditingShot] = useState<number | null>(null);
    // 保存失败的改动留在这里（拆合、移镜头、改镜头），可以重试或放弃；不能因为网关 502 之类的失败让改动悄悄没了。
    const [failed, setFailed] = useState<{ edit: SegmentEdit; success: string; error: string } | null>(null);
    const spec = useMemo(() => {
        try {
            return JSON.parse(specJson) as DramaSegment;
        } catch {
            return undefined;
        }
    }, [specJson]);
    const compileQuery = useQuery({
        queryKey: ["project", projectId, "drama", "compile", shotId, revisionId || ""],
        queryFn: async () => (await compileProjectShot(projectId, shotId)).compiled,
        enabled: open,
        staleTime: 30_000,
    });
    const segmentsQuery = useQuery({
        queryKey: dramaSegmentsKey(projectId, unitId),
        queryFn: async ({ signal }) => (await listDramaSegments(projectId, unitId, signal)).segments,
        enabled: Boolean(unitId),
    });
    // 分镜卡编辑改的是镜头表里的镜头：镜头表装过段（不是草稿）时，有 shot_id 的镜头按镜头表编辑，否则只能改段内文字。
    const shotlistQuery = useQuery({
        queryKey: dramaShotlistKey(projectId, unitId),
        queryFn: async ({ signal }) => (await getDramaShotlist(projectId, unitId, signal)).shotlist,
        enabled: Boolean(unitId),
    });
    const vocabQuery = useQuery({ queryKey: ["drama", "shot-vocab"], queryFn: async ({ signal }) => (await getDramaShotVocab(signal)).vocab, staleTime: Infinity });
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const shotlist = shotlistQuery.data && !shotlistQuery.data.draft ? shotlistQuery.data : undefined;
    const scriptLines = useMemo(() => scriptLineOptions(shotlist?.script), [shotlist?.script]);
    const code = (text: string) => projectSceneCode(text, episode);
    // 本段以工作台当前版本为准，相邻段取服务端分段列表。
    const list = useMemo(() => (segmentsQuery.data || []).map((view) => view.shotId === shotId && spec ? { ...view, spec } : view), [segmentsQuery.data, shotId, spec]);
    const index = list.findIndex((view) => view.shotId === shotId);
    useEffect(() => {
        setEditingShot(null);
        setFailed(null);
    }, [shotId]);
    const save = useMutation({
        mutationFn: (edit: SegmentEdit) => writeDramaSegments(projectId, { unitId, segments: edit.segments, ...(edit.order ? { order: edit.order } : {}), ...(edit.inherit ? { inherit: edit.inherit } : {}), ...(edit.shots?.length ? { shots: edit.shots, shotlistVersion: shotlist?.version } : {}) }),
        onSuccess: async (result, edit) => {
            setFailed(null);
            setEditingShot(null);
            queryClient.setQueryData(dramaSegmentsKey(projectId, unitId), result.segments);
            if (result.check) {
                // 分镜卡编辑后整章重检：问题清掉的镜头不再标；镜头表、进度里的带问题段数一起刷新。
                message.info(result.check.errors ? `已重检：本章还有 ${result.check.errors} 个问题` : "已重检：本章镜头表检查没有问题了");
                void queryClient.invalidateQueries({ queryKey: dramaShotlistKey(projectId, unitId) });
                void queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) });
            }
            await onEdited?.(edit.selectShotId);
        },
        onError: (error) => message.error(`段保存失败：${error instanceof Error ? error.message : "未知错误"}`),
    });
    // 改为武戏 / 改回文戏：服务端按新戏类重切这一节、建生成分段任务（只重写受影响的节），这里跟到写完再刷新本章的段。
    const regenre = useMutation({
        mutationFn: (input: { sectionId: string; genre: SectionGenreTarget; config: typeof effectiveConfig }) =>
            changeChapterSectionGenre({ projectId, chapterId: unitId, sectionId: input.sectionId, genre: input.genre, config: input.config, rule: genreRule }),
        onMutate: (input) => {
            message.open({ key: "section-genre", type: "loading", duration: 0, content: input.genre === "wuxi" ? `正在按武戏重切、重写 ${code(input.sectionId)}…` : `正在按文戏重切、重写 ${code(input.sectionId)}…` });
        },
        onSuccess: async (done, input) => {
            const outcome = done.outcome;
            const label = dramaShotGenreLabel(done.genre);
            if (outcome?.status === "draft") {
                message.open({ key: "section-genre", type: "warning", duration: 10, content: `${code(input.sectionId)} 已按${label}重写（${done.sections.map(code).join("、")}），但镜头表检查有 ${outcome.errors} 个问题，存成了草稿、还没装段：到章节页「生成分段」侧栏处理` });
            } else {
                message.open({ key: "section-genre", type: "success", content: `已把 ${code(input.sectionId)} 改为${label}：重写 ${done.sections.map(code).join("、")}${outcome ? `，本章装成 ${outcome.segments} 段` : ""}` });
            }
            await Promise.all([
                queryClient.invalidateQueries({ queryKey: dramaSegmentsKey(projectId, unitId) }),
                queryClient.invalidateQueries({ queryKey: dramaShotlistKey(projectId, unitId) }),
                queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) }),
            ]);
            await onEdited?.();
        },
        onError: (error) => message.open({ key: "section-genre", type: "error", duration: 8, content: `改戏类失败：${error instanceof Error ? error.message : "未知错误"}（原来的段没有改动）` }),
    });
    if (!spec) return null;
    const result = compileQuery.data?.result;
    const editable = Boolean(durationRule && index >= 0 && !list[index].derived);
    const previous = index > 0 && !list[index - 1].derived ? list[index - 1] : undefined;
    const next = index >= 0 && index < list.length - 1 && !list[index + 1].derived ? list[index + 1] : undefined;
    const takesOf = (id: string) => takeCounts?.[id] || 0;

    /** 写回一次段编辑：成功提示 success；失败把改动和原因留在卡片上（重试或放弃），不吞掉。 */
    const submitEdit = (edit: SegmentEdit, success: string) =>
        save.mutateAsync(edit).then(
            () => { message.success(success); },
            (error: unknown) => { setFailed({ edit, success, error: error instanceof Error ? error.message : "未知错误" }); },
        );
    /** 先在前端算出要写回的段，再按影响确认：被删的段和已有生成结果的段都要说清楚。 */
    const apply = (build: (rule: SegmentDurationRule) => SegmentEdit, success: (edit: SegmentEdit) => string, confirm?: (edit: SegmentEdit) => { title: string; lines: string[]; danger?: boolean }) => {
        if (!durationRule || save.isPending) return;
        let edit: SegmentEdit;
        try {
            edit = build(durationRule);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "段编辑失败");
            return;
        }
        const touched = [...edit.segments.flatMap((segment) => list.find((view) => view.spec.id === segment.id)?.shotId || []), ...edit.removedShotIds];
        const busy = list.find((view) => touched.includes(view.shotId) && busyShotIds?.has(view.shotId));
        if (busy) {
            message.warning(`${busy.spec.id} 正在生成，等它结束再改`);
            return;
        }
        const submit = () => submitEdit(edit, success(edit));
        const stale = touched.filter((id) => !edit.removedShotIds.includes(id) && takesOf(id) > 0).map((id) => list.find((view) => view.shotId === id)?.spec.id || "").filter(Boolean);
        const asked = confirm?.(edit);
        if (!asked && !stale.length) {
            void submit();
            return;
        }
        const lines = [...(asked?.lines || []), ...(stale.length ? [`${stale.join("、")} 已有生成结果，改动后它们对应改动前的段，需要重新生成。`] : [])];
        modal.confirm({
            title: asked?.title || "改动已有生成结果的段？",
            content: <div className="space-y-1.5 text-sm leading-6 text-foreground/62">{lines.map((line) => <p key={line}>{line}</p>)}</div>,
            okText: "确定",
            okButtonProps: asked?.danger ? { danger: true } : undefined,
            cancelText: "取消",
            centered: true,
            onOk: submit,
        });
    };
    const mergeConfirm = (first: DramaSegmentView, second: DramaSegmentView) => (edit: SegmentEdit) => ({
        title: `把 ${second.spec.id} 并入 ${first.spec.id}？`,
        danger: takesOf(second.shotId) > 0,
        lines: [
            `合并后 ${first.spec.id} 共 ${edit.segments[0].shots.length} 个镜头、${edit.segments[0].dur} 秒，${second.spec.id} 删除${takesOf(second.shotId) ? `，它的 ${takesOf(second.shotId)} 个生成结果一起删除，不能撤销` : ""}。`,
            ...(first.spec.scene !== second.spec.scene ? [`两段不是同一场（${first.spec.scene} / ${second.spec.scene}），合并后按「${first.spec.scene}」写。`] : []),
        ],
    });
    const mergedMessage = (edit: SegmentEdit) => `已合并为 ${edit.segments[0].id}（${edit.segments[0].dur} 秒）`;
    const shotActions = (shotIndex: number) => [
        ...(shotIndex > 0 ? [{ key: "split", label: "从这个镜头起拆成新段" }] : []),
        ...(shotIndex === 0 && previous && spec.shots.length > 1 ? [{ key: "prev", label: `移到 ${previous.spec.id} 末尾` }] : []),
        ...(shotIndex === spec.shots.length - 1 && next && spec.shots.length > 1 ? [{ key: "next", label: `移到 ${next.spec.id} 开头` }] : []),
    ];
    const runShotAction = (shotIndex: number, key: string) => {
        if (key === "split") apply((rule) => splitSegment(list, index, shotIndex, rule), (edit) => `已拆成 ${edit.segments.map((segment) => segment.id).join(" 和 ")}`);
        if (key === "prev" || key === "next") apply((rule) => moveEdgeShot(list, index, key, rule), () => `已把镜头移到 ${(key === "prev" ? previous : next)?.spec.id}`);
    };
    const genreTarget = sectionGenreTarget(spec.genre);
    /** 改戏类先确认：写明覆盖哪几段、按什么口径重切、产物会过期。 */
    const askRegenre = () => {
        const span = sectionGenreSpan(list, index);
        if (!spec.section || !span.length || regenre.isPending) return;
        const busy = span.find((view) => busyShotIds?.has(view.shotId));
        if (busy) {
            message.warning(`${busy.spec.id} 正在生成，等它结束再改戏类`);
            return;
        }
        const textModel = effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            message.warning("先在设置里配好可用的文本模型，再改戏类");
            return;
        }
        const confirm = sectionGenreConfirm(list, span, genreTarget, genreRule?.maxSeconds, takesOf, code);
        modal.confirm({
            title: confirm.title,
            content: <div className="space-y-1.5 text-sm leading-6 text-foreground/62">{confirm.lines.map((line) => <p key={line}>{line}</p>)}</div>,
            okText: confirm.okText,
            okButtonProps: { danger: true },
            cancelText: "取消",
            centered: true,
            onOk: () => {
                void regenre.mutateAsync({ sectionId: spec.section || "", genre: genreTarget, config }).catch(() => undefined);
            },
        });
    };
    const shotSeconds = subShotSeconds(spec);
    const lacksReferences = segmentLacksReferences(spec);
    // 带问题装段后挂在本段镜头上的检查 error（以服务端分段列表为准，改完重检后跟着变）。
    const issues: DramaShotIssue[] = (index >= 0 ? list[index].issues : undefined) || [];
    const cardShot = (shot: DramaSubShot) => (shot.shot_id ? shotlist?.shotlist.shots.find((item) => item.id === shot.shot_id) : undefined);
    const runFix = (issue: DramaShotIssue, shotIdOfCard: string, label = issueFixLabel(issue) || "修复") => {
        apply(() => shotFixEdit(list, index, issueFixShots(shotlist?.shotlist.shots || [], issue, shotIdOfCard)), () => `已${label}：${code(issue.lineId || "")}`);
    };
    // 卡 G：按小节的戏类排版。镜头表读到了（不是草稿）才知道这一节有没有存原样输出，没有的标「旧数据」、按统一镜头表显示。
    const sectionKnown = Boolean(shotlistQuery.data && !shotlistQuery.data.draft);
    const layout = sectionLayout(spec, sectionOutputFor(spec, shotlist?.sections), cardShot, sectionKnown);
    const layoutBadge = sectionLayoutBadge(layout);
    // 「补进这个镜头」默认落在前一句所在的镜，剧作上不一定对：本段别的镜头也列出来让人选。
    const addLineTargets = (current: string) =>
        spec.shots.flatMap((sub, shotIndex) => (sub.shot_id && sub.shot_id !== current ? [{ key: sub.shot_id, label: `补进第 ${shotIndex + 1} 个镜头（${subShotLabel(sub, shotSeconds[shotIndex], cardShot(sub)?.action)}）` }] : []));
    const issueList = (onShot: DramaShotIssue[], shotIndex: number, source?: DramaShot, sub?: DramaSubShot) => (
        <ul className="mt-1 space-y-1 pl-0" aria-label={`第 ${shotIndex + 1} 个镜头的检查问题`}>
            {onShot.map((issue, issueIndex) => {
                const fix = editable && source ? issueFixLabel(issue) : undefined;
                const others = fix && issue.fix === "add_line" ? addLineTargets(sub?.shot_id || "") : [];
                return (
                    <li key={`${issue.rule}-${issue.lineId || ""}-${issueIndex}`} className="flex list-none flex-wrap items-center gap-x-2 gap-y-0.5 text-status-error">
                        <span className="font-mono text-[var(--fs-tiny)] text-foreground/45">{issue.rule}</span>
                        <span className="min-w-0 flex-1">{code(issue.message)}</span>
                        {fix && others.length ? (
                            <Space.Compact size="small">
                                <Button size="small" danger disabled={save.isPending} onClick={() => runFix(issue, sub?.shot_id || "")}>
                                    {fix}
                                </Button>
                                <Dropdown trigger={["click"]} disabled={save.isPending} menu={{ items: others, onClick: ({ key }) => runFix(issue, key, `补进第 ${spec.shots.findIndex((item) => item.shot_id === key) + 1} 个镜头`) }}>
                                    <Button size="small" danger icon={<ChevronDown className="size-3.5" />} aria-label="补进本段别的镜头" />
                                </Dropdown>
                            </Space.Compact>
                        ) : fix ? (
                            <Button size="small" danger disabled={save.isPending} onClick={() => runFix(issue, sub?.shot_id || "")}>
                                {fix}
                            </Button>
                        ) : null}
                    </li>
                );
            })}
        </ul>
    );

    return (
        <div className="mb-3 rounded-lg border border-border/70 px-3 py-2.5 text-xs">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
                    <span className="text-[var(--fs-label)] font-semibold">{spec.section ? code(spec.section) : spec.id}</span>
                    <span className={`rounded border px-1.5 py-0.5 text-[var(--fs-tiny)] ${layout.legacy ? "border-border/70 text-foreground/50" : "border-[var(--workspace-accent)]/40 text-foreground/70"}`} title={layout.legacy ? "没有按小节存技能原样输出（卡 F 之前生成、或原样输出没存下）：按统一镜头表显示" : `按${layout.label}版式显示`}>
                        {layoutBadge}
                    </span>
                    <span className="text-foreground/50">
                        {spec.section ? `${spec.id} · ` : ""}
                        {code(spec.scene)} · {spec.dur} 秒 · 段内 {spec.shots.length} 个镜头
                    </span>
                    {issues.length ? <span className="rounded bg-status-error/10 px-1.5 py-0.5 text-[var(--fs-tiny)] font-medium text-status-error">{issues.length} 个问题</span> : null}
                </span>
                <span className="flex flex-wrap items-center gap-1">
                    {editable && previous ? <Button size="small" type="text" disabled={save.isPending} onClick={() => apply((rule) => mergeWithNext(list, index - 1, rule), mergedMessage, mergeConfirm(previous, list[index]))}>并入 {previous.spec.id}</Button> : null}
                    {editable && next ? <Button size="small" type="text" disabled={save.isPending} onClick={() => apply((rule) => mergeWithNext(list, index, rule), mergedMessage, mergeConfirm(list[index], next))}>合并 {next.spec.id}</Button> : null}
                    {editable && spec.section && genreRule ? (
                        <Button size="small" type="text" loading={regenre.isPending} disabled={save.isPending || regenre.isPending} title={genreTarget === "wuxi" ? "这一节按武戏口径重切、用武戏技能重写，覆盖文戏版" : "这一串武戏节按文戏口径重切、用文戏技能重写，覆盖武戏版"} onClick={askRegenre}>
                            {genreTarget === "wuxi" ? "改为武戏" : "改回文戏"}
                        </Button>
                    ) : null}
                    <Button size="small" type="link" onClick={() => setOpen((value) => !value)}>{open ? "收起编译结果" : "生成前看提示词"}</Button>
                </span>
            </div>
            {durationRule ? <p className="mt-1 leading-5 text-foreground/40">{editable ? `段长按视频渠道（${durationRule.label}）取档：拆段、合并、改时长后自动取能提交的段长。` : segmentsQuery.isLoading ? "正在读取本章分段…" : segmentsQuery.isError ? "本章分段读取失败，暂时不能拆合。" : "这一段还不是分段表里的段，不能拆合。"}</p> : null}
            {failed ? (
                <Callout
                    tone="error"
                    role="alert"
                    className="mt-2 px-2.5 py-2"
                    action={<span className="flex gap-1"><Button size="small" type="primary" loading={save.isPending} onClick={() => void submitEdit(failed.edit, failed.success)}>重试</Button><Button size="small" disabled={save.isPending} onClick={() => { setFailed(null); setEditingShot(null); }}>放弃改动</Button></span>}
                >
                    {failedSegmentEditHint(failed.edit, failed.error)}
                </Callout>
            ) : null}
            {lacksReferences ? <Callout tone="warning" className="mt-2 px-2.5 py-2" action={onPickAssets ? <Button size="small" onClick={onPickAssets}>选择资产</Button> : undefined}>{NO_REFERENCE_HINT}</Callout> : null}
            <SectionLayoutHead layout={layout} />
            <ol className="mt-2 space-y-1.5">
                {spec.shots.map((shot, shotIndex) => {
                    const end = spec.shots[shotIndex + 1]?.at ?? spec.dur;
                    const actions = editable ? shotActions(shotIndex) : [];
                    const source = cardShot(shot);
                    const onShot = shotIssues(issues, shot.shot_id);
                    if (editingShot === shotIndex && editable) {
                        // 编辑时这一镜的检查问题仍显示在编辑器上方，不被盖住。
                        const issuesAbove = onShot.length ? issueList(onShot, shotIndex, source, shot) : null;
                        if (source) {
                            return <li key={`${shot.at}-${shotIndex}`}>{issuesAbove}<ShotCardEditor shot={source} vocab={vocabQuery.data} lines={scriptLines} code={code} saving={save.isPending} onCancel={() => setEditingShot(null)} onSave={(next) => apply((rule) => editShotCard(list, index, shotIndex, source, next, rule), () => `已保存 ${spec.id} 第 ${shotIndex + 1} 个镜头`)} /></li>;
                        }
                        return <li key={`${shot.at}-${shotIndex}`}>{issuesAbove}<SubShotEditor shot={shot} seconds={shotSeconds[shotIndex]} saving={save.isPending} onCancel={() => setEditingShot(null)} onSave={(patch) => apply((rule) => editSubShot(list, index, shotIndex, patch, rule), () => `已保存 ${spec.id} 第 ${shotIndex + 1} 个镜头`)} /></li>;
                    }
                    const cardActions = editable ? (
                        <>
                            <Button type="text" size="small" disabled={save.isPending} icon={<Pencil className="size-3.5" />} aria-label={source ? `编辑第 ${shotIndex + 1} 个镜头的分镜卡` : `编辑第 ${shotIndex + 1} 个镜头的画面、台词和时长`} title={source ? "改动作、画面、台词、景别、时长、机位运镜、情绪弧、站位、灯光" : "改画面、台词和时长"} onClick={() => setEditingShot(shotIndex)} />
                            {actions.length ? (
                                <Dropdown trigger={["click"]} disabled={save.isPending} menu={{ items: actions, onClick: ({ key }) => runShotAction(shotIndex, key) }}>
                                    <Button type="text" size="small" icon={<MoreHorizontal className="size-3.5" />} aria-label={`第 ${shotIndex + 1} 个镜头：拆段或移到相邻段`} />
                                </Dropdown>
                            ) : null}
                        </>
                    ) : null;
                    if (!layout.legacy) {
                        return (
                            <li key={`${shot.at}-${shotIndex}`}>
                                <GenreShotCard layout={layout} card={layout.cards[shotIndex]} highlighted={onShot.length > 0} actions={cardActions} assets={cardBoundAssets(shot, spec, boundAssets)}>
                                    {onShot.length ? issueList(onShot, shotIndex, source, shot) : null}
                                </GenreShotCard>
                            </li>
                        );
                    }
                    return (
                        <li key={`${shot.at}-${shotIndex}`} className={`flex gap-2 leading-5 ${onShot.length ? "rounded-md bg-status-error/5 ring-1 ring-status-error/30" : ""}`}>
                            <span className="w-16 shrink-0 tabular-nums text-foreground/45">{shot.at}–{end}s</span>
                            <span className="min-w-0 flex-1 text-foreground/70">{[shot.size, shot.camera].filter(Boolean).join(" · ")}{shot.size || shot.camera ? "：" : ""}{shot.text}{shot.lines?.length ? <span className="block text-foreground/55">{shot.lines.map((line) => `${line.speaker}${line.vo ? "（画外）" : ""}：「${line.text}」`).join(" ")}</span> : null}<SubShotDetails shot={shot} />
                                {onShot.length ? issueList(onShot, shotIndex, source, shot) : null}
                            </span>
                            {cardActions ? <span className="flex shrink-0 items-start">{cardActions}</span> : null}
                        </li>
                    );
                })}
            </ol>
            <SectionLayoutTail layout={layout} />
            {open ? (
                <div className="mt-2 border-t border-border/60 pt-2">
                    {compileQuery.isLoading ? <p className="text-foreground/45">正在编译…</p> : null}
                    {compileQuery.isError ? <p className="text-amber-600 dark:text-amber-400">编译失败：{compileQuery.error instanceof Error ? compileQuery.error.message : "未知错误"}</p> : null}
                    {result ? (
                        <>
                            <div className="flex flex-wrap gap-x-3 gap-y-1 text-foreground/50"><span>{result.model}</span><span>提交 {result.duration} 秒</span><span>{result.chars} 字</span>{estimate ? <span>预估 {estimate}</span> : null}</div>
                            {result.issues.length ? <ul className="mt-1 space-y-0.5">{result.issues.map((issue, issueIndex) => <li key={issueIndex} className={issue.level === "error" ? "text-red-500" : "text-amber-600 dark:text-amber-400"}>{issue.level === "error" ? "不能提交：" : "注意："}{issue.message}</li>)}</ul> : null}
                            {result.files.length ? <p className="mt-1 text-foreground/50">参考：{result.files.map((file) => `${file.tag}=${file.role}`).join("，")}</p> : <p className="mt-1 text-amber-600 dark:text-amber-400">参考：无。{NO_REFERENCE_HINT}{onPickAssets ? "点上方「选择资产」绑定。" : ""}</p>}
                            <pre className="mt-1.5 max-h-56 overflow-auto whitespace-pre-wrap rounded-md bg-surface-active p-2 text-[var(--fs-label)] leading-5 text-foreground/70">{result.prompt}</pre>
                        </>
                    ) : null}
                </div>
            ) : null}
        </div>
    );
}

const NO_REFERENCE_HINT = "此段没有人物/场景参考，人物一致性只靠文字。";

/** 「补进别的镜头」菜单里的一镜：段内起始秒、时长和动作（镜头表的动作，没有时用段里的画面句）开头。 */
function subShotLabel(shot: DramaSubShot, seconds: number, action?: string) {
    const text = (action || shot.text).trim();
    return `${shot.at}s 起 ${seconds} 秒${text ? `，${text.length > 16 ? `${text.slice(0, 16)}…` : text}` : ""}`;
}

/** 由镜头表装出的镜头（2.1）带的表情链、站位、灯光七项和衔接：只读，跟着镜头表重新生成。 */
function SubShotDetails({ shot }: { shot: DramaSubShot }) {
    const transition = shot.transition ? [shot.transition.in && `上一镜→本镜：${shot.transition.in}`, shot.transition.out && `本镜→下一镜：${shot.transition.out}`, shot.transition.cut_point && `切点：${shot.transition.cut_point}`].filter(Boolean).join("；") : "";
    const rows: Array<[string, string]> = [
        ["表情链", [shot.expression, shot.expression_terms?.length ? `核心词：${shot.expression_terms.join("、")}` : ""].filter(Boolean).join("；")],
        ["站位", shot.positions || ""],
        ["灯光", shot.light_full || ""],
        ["衔接", transition],
    ];
    const visible = rows.filter(([, value]) => value);
    if (!visible.length) return null;
    return (
        <dl className="mt-1 grid grid-cols-[3.5rem_minmax(0,1fr)] gap-x-2 gap-y-0.5 rounded-md bg-surface-active px-2 py-1 text-foreground/55" aria-label="镜头表字段（只读）">
            {visible.map(([label, value]) => (
                <div key={label} className="contents">
                    <dt className="text-foreground/40">{label}</dt>
                    <dd className="m-0 min-w-0 break-words">{value}</dd>
                </div>
            ))}
        </dl>
    );
}

/** 段内一个镜头的编辑：画面、台词（每行一句「说话人：台词」）、时长。 */
function SubShotEditor({ shot, seconds, saving, onSave, onCancel }: { shot: DramaSubShot; seconds: number; saving: boolean; onSave: (patch: { text: string; lines: DramaLine[]; seconds: number }) => void; onCancel: () => void }) {
    const [text, setText] = useState(shot.text);
    const [lines, setLines] = useState(() => formatDialogue(shot.lines));
    const [length, setLength] = useState<number | null>(seconds);
    return (
        <div className="grid gap-1.5 rounded-md border border-border/70 bg-surface-active p-2">
            <label className="grid gap-1"><span className="text-foreground/50">画面</span><Input.TextArea autoFocus value={text} onChange={(event) => setText(event.target.value)} autoSize={{ minRows: 2, maxRows: 6 }} /></label>
            <label className="grid gap-1"><span className="text-foreground/50">台词（每行一句「说话人：台词」，画外音写成「说话人（画外）：台词」）</span><Input.TextArea value={lines} onChange={(event) => setLines(event.target.value)} autoSize={{ minRows: 1, maxRows: 6 }} placeholder="林小满：七床的人呢？" /></label>
            <div className="flex flex-wrap items-center justify-between gap-2">
                <label className="flex items-center gap-1.5"><span className="text-foreground/50">时长</span><InputNumber size="small" min={0.5} max={60} step={0.5} value={length} onChange={setLength} className="w-20" /><span className="text-foreground/50">秒</span></label>
                <span className="flex gap-1.5">
                    <Button size="small" onClick={onCancel} disabled={saving}>取消</Button>
                    <Button size="small" type="primary" loading={saving} disabled={!text.trim() || !length} onClick={() => onSave({ text, lines: parseDialogue(lines), seconds: length || seconds })}>保存</Button>
                </span>
            </div>
        </div>
    );
}

type BriefItem = { id: string; content: string; quote?: string; date?: string; segments?: string[]; status: string };

/** 看片反馈：带时间码的一句意见记成需求文档里的 R- 条目，指向哪几段；不新开段，只等那段重做。 */
function DramaFeedbackForm({ projectId, segments }: { projectId: string; segments: DramaSegmentProgress[] }) {
    const { message } = App.useApp();
    const invalidate = useInvalidateDrama(projectId);
    const [timecode, setTimecode] = useState("");
    const [segmentId, setSegmentId] = useState("");
    const [content, setContent] = useState("");
    const mutation = useMutation({
        mutationFn: async () => {
            const { document } = await getDramaBrief(projectId);
            const items = Array.isArray(document.items) ? (document.items as BriefItem[]) : [];
            const next = items.reduce((max, item) => Math.max(max, Number(/^R-(\d{3})$/.exec(item.id)?.[1] || 0)), 0) + 1;
            const today = new Date().toISOString().slice(0, 10);
            const text = content.trim();
            const item: BriefItem = { id: `R-${String(next).padStart(3, "0")}`, content: timecode.trim() ? `[${timecode.trim()}] ${text}` : text, quote: text, date: today, status: "待处理", ...(segmentId ? { segments: [segmentId] } : {}) };
            await putDramaBrief(projectId, { ...document, items: [...items, item] });
            return item;
        },
        onSuccess: (item) => { setContent(""); setTimecode(""); void invalidate(); message.success(`已记为 ${item.id}`); },
        onError: (error) => message.error(error instanceof Error ? error.message : "反馈没记上"),
    });
    return (
        <div className="mt-4 border-t border-border/60 pt-3">
            <div className="text-xs font-medium">看片反馈</div>
            <p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">一条意见记成需求文档里的一个 R- 条目，指到对应的段；Agent 和制作页都按它重做那一段。</p>
            <div className="mt-2 flex flex-wrap gap-2">
                <Input size="small" className="!w-24" value={timecode} onChange={(event) => setTimecode(event.target.value)} placeholder="00:12" aria-label="时间码" />
                <select className="h-6 rounded-md border border-border/70 bg-transparent px-1.5 text-xs" value={segmentId} onChange={(event) => setSegmentId(event.target.value)} aria-label="对应的段">
                    <option value="">不指定段</option>
                    {segments.map((segment) => <option key={segment.shotId} value={segment.segmentId}>{segment.segmentId} {segment.title}</option>)}
                </select>
                <Input size="small" className="min-w-48 flex-1" value={content} onChange={(event) => setContent(event.target.value)} placeholder="这一下哪里不对、要改成什么" maxLength={300} />
                <Button size="small" disabled={!content.trim()} loading={mutation.isPending} onClick={() => mutation.mutate()}>记下</Button>
            </div>
        </div>
    );
}

export const dramaBibleKey = (projectId: string) => ["project", projectId, "drama", "bible"] as const;
export const dramaChecklistAssetsKey = (projectId: string) => ["project", projectId, "assets", "drama-checklist"] as const;

const CHECKLIST_GROUPS: Array<{ kind: ChecklistKind; label: string }> = [
    { kind: "character", label: "角色" },
    { kind: "location", label: "场景" },
    { kind: "prop", label: "道具" },
];

/**
 * 角色与资产页的素材清单：连戏表里每个人物造型、场景、道具一行，机器能查的门禁（三视图、锁定卡、音色、空景、道具图）
 * 全过以后才能人判「过门禁」；勾选写回连戏表，概览的「参考齐了」按它算。
 */
export function DramaAssetChecklist({ projectId }: { projectId: string }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const invalidate = useInvalidateDrama(projectId);
    const [expanded, setExpanded] = useState(false);
    const bibleQuery = useQuery({ queryKey: dramaBibleKey(projectId), queryFn: async () => (await getDramaBible(projectId)).document });
    const assetsQuery = useQuery({ queryKey: dramaChecklistAssetsKey(projectId), queryFn: async () => (await listProjectAssets(projectId)).assets });
    const items = useMemo(() => buildAssetChecklist(bibleQuery.data, assetsQuery.data || []), [bibleQuery.data, assetsQuery.data]);
    const summary = checklistSummary(items);
    const save = useMutation({
        mutationFn: (document: DramaBible) => putDramaBible(projectId, document),
        onSuccess: async ({ document }) => {
            queryClient.setQueryData(dramaBibleKey(projectId), document);
            await invalidate();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "连戏表没保存上"),
    });
    if (bibleQuery.isLoading || assetsQuery.isLoading) return <section className="mb-3 rounded-xl border border-border/70 bg-surface px-4 py-3 text-xs text-foreground/45">正在读取素材清单…</section>;
    if (bibleQuery.isError) return <section className="mb-3 rounded-xl border border-border/70 bg-surface px-4 py-3 text-xs text-foreground/55">素材清单读取失败：{bibleQuery.error instanceof Error ? bibleQuery.error.message : "未知错误"} <Button size="small" type="link" onClick={() => void bibleQuery.refetch()}>重试</Button></section>;
    const assets = assetsQuery.data || [];
    const draftable = assets.some((asset) => asset.category === "character" || asset.category === "environment" || asset.category === "prop");
    if (!items.length) {
        return (
            <section className="mb-3 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/70 bg-surface px-4 py-3">
                <div className="min-w-0">
                    <div className="text-xs font-medium">素材清单（连戏表）</div>
                    <p className="mt-0.5 text-[var(--fs-label)] leading-5 text-foreground/48">还没有清单。通常由 Agent 的 yz-cast / yz-world 写；也可以先按已确认的角色、场景、道具起草，再逐项过门禁。</p>
                </div>
                <Button size="small" disabled={!draftable} loading={save.isPending} onClick={() => save.mutate(draftBibleFromAssets(bibleQuery.data, assets))}>按已确认资产起草清单</Button>
            </section>
        );
    }
    const pending = items.filter((item) => !item.checked);
    const visible = expanded ? items : pending.slice(0, 6);
    return (
        <section className="mb-3 rounded-xl border border-border/70 bg-surface px-4 py-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0">
                    <div className="flex items-center gap-2 text-xs font-medium">素材清单（连戏表）<span className="rounded bg-foreground/[.06] px-1.5 py-0.5 text-[var(--fs-tiny)] tabular-nums text-foreground/55">过门禁 {summary.checked}/{summary.total}</span></div>
                    <p className="mt-0.5 text-[var(--fs-label)] text-foreground/48">{pending.length ? `还有 ${pending.length} 项没过门禁，其中 ${pending.filter((item) => item.ready).length} 项文件已齐、等人判。` : "清单每项都过了门禁。"}</p>
                </div>
                <Button size="small" type="link" onClick={() => setExpanded((value) => !value)}>{expanded ? "只看没过的" : `展开全部 ${items.length} 项`}</Button>
            </div>
            {visible.length ? CHECKLIST_GROUPS.map((group) => {
                const rows = visible.filter((item) => item.kind === group.kind);
                if (!rows.length) return null;
                return (
                    <div key={group.kind} className="mt-2">
                        <div className="text-[var(--fs-tiny)] font-medium text-foreground/40">{group.label}</div>
                        <ul>{rows.map((item) => <DramaChecklistRow key={item.key} item={item} saving={save.isPending} onToggle={(checked) => bibleQuery.data && save.mutate(setChecklistChecked(bibleQuery.data, item.key, checked))} />)}</ul>
                    </div>
                );
            }) : null}
        </section>
    );
}

function DramaChecklistRow({ item, saving, onToggle }: { item: ChecklistItem; saving: boolean; onToggle: (checked: boolean) => void }) {
    const missing = item.checks.filter((check) => !check.ok);
    return (
        <li className="flex flex-wrap items-start gap-x-3 gap-y-1 border-t border-border/50 py-2 first:border-t-0">
            <Checkbox className="mt-0.5" checked={item.checked} disabled={saving || (!item.checked && !item.ready)} onChange={(event) => onToggle(event.target.checked)} aria-label={`${item.name}${item.variant ? ` ${item.variant}` : ""} 过门禁`}>
                <span className="text-xs">过门禁</span>
            </Checkbox>
            <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2 text-xs">
                    <span className="font-medium">{item.name}{item.variant ? ` · ${item.variant}` : ""}</span>
                    <span className="text-[var(--fs-tiny)] text-foreground/40">{item.asset ? `资产：${item.asset.title}` : "资产库里没找到对应资产"}</span>
                </div>
                <div className="mt-1 flex flex-wrap gap-1">
                    {item.checks.map((check) => (
                        <span key={check.label} title={check.detail} className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[var(--fs-tiny)] ${check.ok ? "bg-foreground/[.06] text-foreground/60" : "bg-amber-500/10 text-amber-600 dark:text-amber-400"}`}>
                            {check.ok ? <CheckCircle2 className="size-3" /> : <CircleAlert className="size-3" />}{check.label}
                        </span>
                    ))}
                </div>
                {item.lockCard.length ? <p className="mt-1 line-clamp-2 text-[var(--fs-tiny)] leading-4 text-foreground/45">锁定卡：{item.lockCard.join(" · ")}</p> : null}
                {item.kind === "character" && (item.voice || item.voiceShort) ? <p className="mt-0.5 text-[var(--fs-tiny)] text-foreground/45">音色：长版{item.voice ? "已有" : "缺"} · 短版{item.voiceShort ? "已有" : "缺"}</p> : null}
                {item.checked && missing.length ? <p className="mt-0.5 text-[var(--fs-tiny)] text-amber-600 dark:text-amber-400">已勾过门禁，但{missing.map((check) => check.label).join("、")}没过：{missing[0].detail}</p> : !item.checked && missing.length ? <p className="mt-0.5 text-[var(--fs-tiny)] text-foreground/45">{missing[0].detail}</p> : null}
            </div>
        </li>
    );
}

/** 一条候选的三项证据现状（抽帧、人脸，有台词时加台词转写）。 */
export function takeEvidenceStatus(evidence: DramaEvidence[], artifactId: string, hasDialogue: boolean) {
    const items = evidence.filter((item) => item.artifactId === artifactId);
    const required: Array<{ kind: DramaEvidenceKind; label: string; passed: boolean | null }> = [
        { kind: "frames", label: "抽帧", passed: latestOf(items, "frames")?.passed ?? null },
        { kind: "faces", label: "人脸", passed: latestOf(items, "faces")?.passed ?? null },
        ...(hasDialogue ? [{ kind: "transcript" as const, label: "台词", passed: latestOf(items, "transcript")?.passed ?? null }] : []),
    ];
    const transcript = latestOf(items, "transcript");
    const metrics = transcript ? parseEvidenceMetrics(transcript) : {};
    const cer = typeof metrics.cer === "number" ? metrics.cer : undefined;
    return { required, passedAll: required.every((item) => item.passed === true), cer };
}

const COMPARE_LIMIT = 4;

/**
 * 候选并排：同一段的几条视频候选放在一起同步播放，每条标出三项证据和字错率；
 * 选定仍走证据面板（三项都过 + 一句理由），这里只负责比较和跳过去。
 */
export function DramaTakeCompare({ projectId, shotId, artifacts, activeId, onOpen }: { projectId: string; shotId: string; artifacts: ShotArtifact[]; activeId?: string; onOpen: (artifactId: string) => void }) {
    const [open, setOpen] = useState(false);
    const takes = artifacts.filter((item) => item.type === "video" && item.status === "ready" && item.resourceId);
    const [chosen, setChosen] = useState<string[]>([]);
    const progressQuery = useDramaProgress(projectId);
    const evidenceQuery = useQuery({ queryKey: dramaEvidenceKey(projectId), queryFn: async ({ signal }) => (await listDramaEvidence(projectId, signal)).evidence, enabled: open });
    const videos = useRef(new Map<string, HTMLVideoElement>());
    const hasDialogue = Boolean(progressQuery.data?.segments.find((item) => item.shotId === shotId)?.hasDialogue);
    if (takes.length < 2) return null;
    const defaults = [...takes.filter((item) => item.selected), ...takes.filter((item) => !item.selected)].slice(0, COMPARE_LIMIT).map((item) => item.id);
    const shown = (chosen.length ? chosen : defaults).map((id) => takes.find((item) => item.id === id)).filter((item): item is ShotArtifact => Boolean(item));
    const playAll = () => videos.current.forEach((video) => { video.currentTime = 0; void video.play().catch(() => undefined); });
    const pauseAll = () => videos.current.forEach((video) => video.pause());
    const toggle = (id: string) => setChosen((current) => {
        const base = current.length ? current : defaults;
        if (base.includes(id)) return base.length > 2 ? base.filter((item) => item !== id) : base;
        return base.length >= COMPARE_LIMIT ? base : [...base, id];
    });
    return (
        <>
            <Button size="small" block onClick={() => setOpen(true)}>并排对比 {takes.length} 条候选</Button>
            <AppModal open={open} title="候选并排对比" footer={null} centered width="min(1200px, calc(100vw - 32px))" onCancel={() => { pauseAll(); setOpen(false); }}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-xs text-foreground/50">同一段最多并排 {COMPARE_LIMIT} 条。看同一个人、台词、崩坏；选定要回到证据面板写理由。</p>
                    <div className="flex gap-1.5"><Button size="small" icon={<Play className="size-3.5" />} onClick={playAll}>从头同步播放</Button><Button size="small" icon={<Pause className="size-3.5" />} onClick={pauseAll}>全部暂停</Button></div>
                </div>
                {takes.length > COMPARE_LIMIT ? (
                    <div className="mt-2 flex flex-wrap gap-1.5 text-[var(--fs-label)]">
                        {takes.map((take) => <button key={take.id} type="button" aria-pressed={shown.some((item) => item.id === take.id)} className={`rounded px-2 py-0.5 ${shown.some((item) => item.id === take.id) ? "bg-surface-active font-medium" : "bg-foreground/[.05] text-foreground/55"}`} onClick={() => toggle(take.id)}>v{take.version}{take.selected ? " · 选定" : ""}</button>)}
                    </div>
                ) : null}
                <div className={`mt-3 grid gap-3 ${shown.length > 2 ? "sm:grid-cols-2 xl:grid-cols-4" : "sm:grid-cols-2"}`}>
                    {shown.map((take) => {
                        const status = takeEvidenceStatus(evidenceQuery.data || [], take.id, hasDialogue);
                        return (
                            <div key={take.id} className={`rounded-lg border p-2 ${take.id === activeId ? "border-[var(--workspace-accent)]/60" : "border-border/70"}`}>
                                <video
                                    ref={(element) => { if (element) videos.current.set(take.id, element); else videos.current.delete(take.id); }}
                                    className="aspect-[9/16] max-h-[52vh] w-full rounded bg-black object-contain"
                                    src={resourceFileUrl(take.resourceId!)}
                                    controls
                                    playsInline
                                    preload="metadata"
                                    aria-label={`候选 v${take.version}`}
                                />
                                <div className="mt-2 flex items-center justify-between gap-2 text-xs"><span className="font-medium">v{take.version}{take.selected ? " · 当前选定" : ""}</span>{status.cer !== undefined ? <span className="tabular-nums text-foreground/50">字错率 {(status.cer * 100).toFixed(1)}%</span> : null}</div>
                                <div className="mt-1 flex flex-wrap gap-1">
                                    {status.required.map((item) => <span key={item.kind} className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[var(--fs-tiny)] ${item.passed === true ? "bg-foreground/[.06] text-foreground/60" : item.passed === false ? "bg-red-500/10 text-red-600 dark:text-red-400" : "bg-foreground/[.04] text-foreground/40"}`}>{item.passed === true ? <CheckCircle2 className="size-3" /> : item.passed === false ? <CircleAlert className="size-3" /> : <Circle className="size-3" />}{item.label}{item.passed === null ? "未做" : item.passed ? "过" : "未过"}</span>)}
                                </div>
                                <Button className="mt-2" size="small" block type={status.passedAll && !take.selected ? "primary" : "default"} onClick={() => { pauseAll(); setOpen(false); onOpen(take.id); }}>{take.selected ? "看这条的证据" : "看证据并选这条"}</Button>
                            </div>
                        );
                    })}
                </div>
            </AppModal>
        </>
    );
}
