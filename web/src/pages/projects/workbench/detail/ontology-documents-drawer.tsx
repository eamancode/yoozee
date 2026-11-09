import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, Segmented, Tabs } from "antd";
import { Copy, Wrench } from "lucide-react";

import { AppDrawer } from "@/components/ui/product/app-drawer";
import { ontologyDirectorCard } from "@/lib/ontology-card";
import { AppModal } from "@/components/ui/product/app-modal";
import { EmptyState } from "@/components/ui/product/empty-state";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { getOntologyDocuments, listDramaSegments, ontologyDocumentsKey, updateOntologyBible, type DramaSegmentView, type OntologyDocuments } from "@/services/api/project-drama";
import type { ProjectDetail } from "@/services/api/projects";
import { useConfigStore } from "@/stores/use-config-store";

import { repairOntologyUnit } from "./project-chapter-ai";

type Props = { detail: ProjectDetail; open: boolean; onClose: () => void };

/**
 * 本体论导演分镜（流水线一）的文档侧栏：世界圣经卡（可改，存新版本）、立项待确认清单，按集看七部分、集交接卡和单元段；
 * 单元段可复制粘贴提示词原文、写症状返修。生成本身在章节页「生成分段」里跑（流水线一自动用本体论导演）。
 */
export function OntologyDocumentsDrawer({ detail, open, onClose }: Props) {
    const projectId = detail.project.id;
    const docs = useQuery({ queryKey: ontologyDocumentsKey(projectId), queryFn: ({ signal }) => getOntologyDocuments(projectId, signal), enabled: open });
    const units = useMemo(() => detail.units.slice().sort((a, b) => a.position - b.position), [detail.units]);
    const generated = units.filter((unit) => docs.data?.episodes.some((doc) => doc.unitId === unit.id));
    const items = [
        { key: "bible", label: "世界圣经卡", children: <BiblePanel projectId={projectId} docs={docs.data} /> },
        ...generated.map((unit) => ({ key: unit.id, label: unit.title, children: <EpisodePanel detail={detail} unitId={unit.id} docs={docs.data} /> })),
    ];
    return (
        <AppDrawer open={open} onClose={onClose} title="本体论导演文档" width={880}>
            {docs.isLoading ? <p className="text-[var(--fs-label)] text-foreground/55">正在读取…</p> : docs.isError ? <EmptyState size="compact" title="读取失败" description={docs.error instanceof Error ? docs.error.message : "请稍后再试"} /> : <Tabs items={items} />}
        </AppDrawer>
    );
}

function DocumentText({ text }: { text: string }) {
    return <pre className="thin-scrollbar max-h-[60vh] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-surface-active p-3 text-[var(--fs-label)] leading-6">{text}</pre>;
}

function BiblePanel({ projectId, docs }: { projectId: string; docs?: OntologyDocuments }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [draft, setDraft] = useState("");
    const [editing, setEditing] = useState(false);
    useEffect(() => {
        if (!editing) setDraft(docs?.bible?.content || "");
    }, [docs?.bible?.content, editing]);
    const save = useMutation({
        mutationFn: () => updateOntologyBible(projectId, draft),
        onSuccess: async () => {
            setEditing(false);
            message.success("世界圣经卡已保存为新版本，只影响之后生成的集");
            await queryClient.invalidateQueries({ queryKey: ontologyDocumentsKey(projectId) });
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "保存失败"),
    });
    if (!docs?.bible && !editing) {
        return <EmptyState size="compact" title="还没有世界圣经卡" description="第一次在章节页「生成分段」时，本体论导演会先立项写出世界圣经卡。" />;
    }
    return (
        <div className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-2">
                <span className="text-[var(--fs-label)] text-foreground/55">{docs?.bible ? `第 ${docs.bible.version} 版` : "新建"}；不变量（身份短串、声线锁、通用基准段、场域锚句）会逐字进每条提示词，改动只影响之后生成的集。</span>
                {editing ? (
                    <span className="flex shrink-0 gap-2">
                        <Button onClick={() => setEditing(false)} disabled={save.isPending}>取消</Button>
                        <Button type="primary" loading={save.isPending} disabled={!draft.trim()} onClick={() => save.mutate()}>保存新版本</Button>
                    </span>
                ) : (
                    <Button className="shrink-0" onClick={() => setEditing(true)}>编辑</Button>
                )}
            </div>
            {editing ? <Input.TextArea value={draft} onChange={(event) => setDraft(event.target.value)} autoSize={{ minRows: 16, maxRows: 32 }} aria-label="世界圣经卡" /> : <DocumentText text={docs?.bible?.content || ""} />}
            {docs?.setup?.content ? (
                <details className="rounded-md border border-border p-3">
                    <summary className="cursor-pointer text-[var(--fs-label)]">立项待确认清单与补图建议</summary>
                    <div className="mt-2"><DocumentText text={docs.setup.content} /></div>
                </details>
            ) : null}
        </div>
    );
}

function EpisodePanel({ detail, unitId, docs }: { detail: ProjectDetail; unitId: string; docs?: OntologyDocuments }) {
    const [view, setView] = useState<"segments" | "episode" | "handoff">("segments");
    const episode = docs?.episodes.find((doc) => doc.unitId === unitId);
    const handoff = docs?.handoffs.find((doc) => doc.unitId === unitId);
    return (
        <div className="flex flex-col gap-3">
            <Segmented value={view} onChange={(value) => setView(value as typeof view)} options={[{ value: "segments", label: "单元段" }, { value: "episode", label: "七部分" }, { value: "handoff", label: "集交接卡" }]} />
            {view === "segments" ? <SegmentList detail={detail} unitId={unitId} episode={episode?.content || ""} /> : null}
            {view === "episode" ? (episode ? <DocumentText text={episode.content} /> : <EmptyState size="compact" title="本集还没有七部分" />) : null}
            {view === "handoff" ? (handoff ? <DocumentText text={handoff.content} /> : <EmptyState size="compact" title="本集还没有交接卡" />) : null}
        </div>
    );
}

function SegmentList({ detail, unitId, episode }: { detail: ProjectDetail; unitId: string; episode: string }) {
    const projectId = detail.project.id;
    const segments = useQuery({ queryKey: ontologySegmentsKey(projectId, unitId), queryFn: ({ signal }) => listDramaSegments(projectId, unitId, signal) });
    const [repairing, setRepairing] = useState<DramaSegmentView | null>(null);
    if (segments.isLoading) return <p className="text-[var(--fs-label)] text-foreground/55">正在读取…</p>;
    // 读取失败不能显示成空态：否则会误导用户重新生成（要花钱）。
    if (segments.isError) {
        return <EmptyState size="compact" title="单元段读取失败" description={segments.error instanceof Error ? segments.error.message : "请稍后再试"} action={<Button onClick={() => void segments.refetch()}>重试</Button>} />;
    }
    const units = (segments.data?.segments || []).filter((segment) => segment.spec.verbatim);
    if (!units.length) return <EmptyState size="compact" title="本集没有本体论单元段" description="在章节页对本集「生成分段」后会出现在这里。" />;
    return (
        <div className="flex flex-col gap-3">
            {units.map((segment) => (
                <SegmentCard key={segment.shotId} segment={segment} card={ontologyDirectorCard(episode, segment.spec.title || "")} onRepair={() => setRepairing(segment)} />
            ))}
            {repairing ? <RepairModal detail={detail} unitId={unitId} segment={repairing} onClose={() => setRepairing(null)} /> : null}
        </div>
    );
}

function ontologySegmentsKey(projectId: string, unitId: string) {
    // 放在 ["project", projectId] 下：生成分段、返修收尾后刷新项目时一起失效。
    return ["project", projectId, "ontology-segments", unitId] as const;
}

function CopyButton({ text, label }: { text: string; label: string }) {
    const { message } = App.useApp();
    const copy = async () => {
        try {
            await navigator.clipboard.writeText(text);
            message.success(`${label}已复制`);
        } catch {
            message.error("复制失败，请手动选中复制");
        }
    };
    return <Button size="small" icon={<Copy className="size-3.5" />} onClick={copy} disabled={!text}>复制{label}</Button>;
}

function SegmentCard({ segment, card, onRepair }: { segment: DramaSegmentView; card: string; onRepair: () => void }) {
    const verbatim = segment.spec.verbatim || "";
    return (
        <section className="rounded-md border border-border p-3">
            <header className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <span className="text-sm font-semibold">{segment.spec.id} · {segment.spec.title} · {segment.spec.dur} 秒 · 参考图 {segment.spec.refs?.length || 0} 张</span>
                <Button size="small" icon={<Wrench className="size-3.5" />} onClick={onRepair}>返修</Button>
            </header>
            <div className="grid gap-3 lg:grid-cols-2">
                <div className="flex min-w-0 flex-col gap-2">
                    <div className="flex items-center justify-between gap-2"><span className="text-[var(--fs-label)] text-foreground/60">导演分镜卡</span><CopyButton text={card} label="导演分镜卡" /></div>
                    {card ? <DocumentText text={card} /> : <p className="text-[var(--fs-label)] text-foreground/55">七部分里没有找到这张单元卡。</p>}
                </div>
                <div className="flex min-w-0 flex-col gap-2">
                    <div className="flex items-center justify-between gap-2"><span className="text-[var(--fs-label)] text-foreground/60">粘贴提示词（{[...verbatim].length} 字符）</span><CopyButton text={verbatim} label="粘贴提示词" /></div>
                    <DocumentText text={verbatim} />
                </div>
            </div>
        </section>
    );
}

function RepairModal({ detail, unitId, segment, onClose }: { detail: ProjectDetail; unitId: string; segment: DramaSegmentView; onClose: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const effectiveConfig = useDramaModelConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const [symptom, setSymptom] = useState("");
    const repair = useMutation({
        mutationFn: async () => {
            const textModel = effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
            const config = { ...effectiveConfig, model: textModel, textModel };
            if (!isAiConfigReady(config, textModel)) throw new Error("还没配置可用的文本模型，请先到设置里选择");
            return repairOntologyUnit({ projectId: detail.project.id, unitId, shotId: segment.shotId, symptom: symptom.trim(), config });
        },
        onMutate: () => {
            message.info(`返修 ${segment.spec.title} 已开始，只替换这一段`);
        },
        onSuccess: async () => {
            message.success(`${segment.spec.title} 已返修`);
            await Promise.all([
                queryClient.invalidateQueries({ queryKey: ontologySegmentsKey(detail.project.id, unitId) }),
                queryClient.invalidateQueries({ queryKey: ontologyDocumentsKey(detail.project.id) }),
                queryClient.invalidateQueries({ queryKey: ["project", detail.project.id] }),
            ]);
            onClose();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "返修失败"),
    });
    return (
        <AppModal open title={`返修 ${segment.spec.title}`} onCancel={onClose} okText="开始返修" okButtonProps={{ disabled: !symptom.trim(), loading: repair.isPending }} onOk={() => repair.mutate()} cancelButtonProps={{ disabled: repair.isPending }}>
            <p className="mb-2 text-[var(--fs-label)] text-foreground/60">写清翻车症状，例如「第3镜林夏的脸和第1集不一样」「动作没做完」「多了一个路人」。本体论导演会诊断、回到责任环节重写这张单元卡，只替换这一段；这一段已有的视频会标为过期。</p>
            <Input.TextArea value={symptom} onChange={(event) => setSymptom(event.target.value)} autoSize={{ minRows: 4, maxRows: 10 }} maxLength={2000} showCount aria-label="翻车症状" />
        </AppModal>
    );
}
