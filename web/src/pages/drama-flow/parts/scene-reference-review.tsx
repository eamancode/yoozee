import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Image, Input, Select } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { dramaSegmentsKey, useInvalidateDrama } from "@/pages/projects/workbench/detail/drama-panels";
import { compileProjectShot, getSceneReferenceCandidate, getSceneReferenceReview, updateSceneReference, type DramaSegmentView, type SceneFacts, type SceneReferenceReview } from "@/services/api/project-drama";
import type { ProjectAsset } from "@/services/api/projects";
import { resourceFileUrl } from "@/services/api/resources";

import { unitWorkspaceKey } from "../hooks/use-flow-data";
import { extractSceneFacts } from "../model/scene-facts";

type Props = { projectId: string; segment: DramaSegmentView; assets: ProjectAsset[]; textModel: string; videoModel: string; onClose: () => void };

export function SceneReferenceReviewDialog(props: Props) {
    const query = useQuery({
        queryKey: ["project", props.projectId, "scene-reference", props.segment.shotId, props.segment.revisionId],
        queryFn: async ({ signal }) => (await getSceneReferenceReview(props.projectId, props.segment.shotId, signal)).review,
        staleTime: 0,
    });
    if (query.data) return <LoadedSceneReview key={query.data.revisionId} {...props} review={query.data} />;
    return (
        <AppModal open title="核对场景与提示词" onCancel={props.onClose} footer={null}>
            {query.isError ? <Button onClick={() => void query.refetch()}>重新读取场景</Button> : <p>正在读取场景参考图…</p>}
        </AppModal>
    );
}

function LoadedSceneReview({ projectId, segment, assets, textModel, videoModel, onClose, review }: Props & { review: SceneReferenceReview }) {
    const client = useQueryClient();
    const invalidate = useInvalidateDrama(projectId);
    const config = useDramaModelConfig();
    const [sceneId, setSceneId] = useState(review.expected.sceneId || (review.choices?.length === 1 ? review.choices[0].sceneId || "" : ""));
    const [versionId, setVersionId] = useState(review.current.versionId || review.expected.versionId || "");
    const [textOnly, setTextOnly] = useState(review.textOnly);
    const [sections, setSections] = useState(review.sections || {});
    const [shots, setShots] = useState(review.shots || []);
    const [draft, setDraft] = useState<{ resourceId?: string; factsRevision?: string; facts: SceneFacts }>();
    const [ackScope, setAckScope] = useState<string>();
    const [showCompiled, setShowCompiled] = useState(false);
    const [saveError, setSaveError] = useState("");
    const choices = new Map(assets.filter((asset) => asset.category === "environment" && asset.primaryVersionId).map((asset) => [asset.primaryVersionId!, asset.title]));
    if (review.current.versionId) choices.set(review.current.versionId, `${review.current.label || "场景"} · 当前固定版本`);
    if (review.expected.versionId && !choices.has(review.expected.versionId)) choices.set(review.expected.versionId, review.expected.label || "本场默认版本");
    const candidate = useQuery({
        queryKey: ["project", projectId, "scene-reference-image", segment.shotId, versionId],
        queryFn: async ({ signal }) => (await getSceneReferenceCandidate(projectId, segment.shotId, versionId, signal)).candidate,
        enabled: Boolean(versionId && !textOnly),
    });
    const image = textOnly ? undefined : candidate.data;
    const currentScope = JSON.stringify([sceneId, versionId, image?.resourceId, image?.factsRevision, textOnly]);
    const ack = ackScope === currentScope;
    const setAck = (checked: boolean) => setAckScope(checked ? currentScope : undefined);
    const alignedDraft = draft && draft.resourceId === image?.resourceId ? draft : undefined;
    const facts = alignedDraft?.facts || image?.facts || {};
    const extraction = useMutation({
        mutationFn: async () => {
            if (!image?.resourceId) throw new Error("场景参考图不可用");
            const resourceId = image.resourceId;
            const extracted = await client.fetchQuery({
                queryKey: ["project", projectId, "scene-facts-draft", resourceId],
                queryFn: () => extractSceneFacts(projectId, image, config, textModel),
                staleTime: Infinity,
                gcTime: 30 * 60 * 1000,
                retry: false,
            });
            return { resourceId, factsRevision: image.factsRevision, facts: extracted };
        },
        onSuccess: (next) => {
            setDraft(next);
            setAck(false);
        },
    });
    const compiled = useQuery({
        queryKey: ["project", projectId, "drama", "compile", segment.shotId, review.revisionId, videoModel],
        queryFn: async () => (await compileProjectShot(projectId, segment.shotId, { channelModel: videoModel })).compiled,
        enabled: showCompiled,
    });
    const save = useMutation({
        mutationFn: async () => {
            if (!ack || !sceneId || (!textOnly && !image?.resourceId)) throw new Error("请先核对本段场景与描述");
            return updateSceneReference(projectId, segment.shotId, {
                revisionId: review.revisionId,
                sceneId,
                versionId: textOnly ? "" : versionId,
                resourceId: textOnly ? "" : image?.resourceId || "",
                textOnly,
                sections: Object.fromEntries(Object.entries(sections).filter(([name, text]) => text !== review.sections?.[name])),
                shots: Object.fromEntries(shots.map((text, i) => [i, text] as const).filter(([i, text]) => text !== review.shots?.[i])),
                facts: textOnly ? undefined : alignedDraft?.facts,
                factsRevision: alignedDraft?.factsRevision,
            });
        },
        onSuccess: async () => {
            await Promise.all([
                invalidate(),
                client.invalidateQueries({ queryKey: dramaSegmentsKey(projectId, segment.unitId) }),
                client.invalidateQueries({ queryKey: unitWorkspaceKey(projectId, segment.unitId) }),
                client.invalidateQueries({ queryKey: ["project", projectId, "scene-reference"] }),
                client.invalidateQueries({ queryKey: ["project", projectId, "scene-reference-image"] }),
                client.invalidateQueries({ queryKey: ["project", projectId, "drama", "compile"] }),
            ]);
            onClose();
        },
        onError: (error) => setSaveError(error instanceof Error ? error.message : "场景保存失败"),
    });
    const busy = save.isPending || extraction.isPending;
    function setFacts(next: SceneFacts) {
        setDraft({ resourceId: image?.resourceId, factsRevision: alignedDraft?.factsRevision || image?.factsRevision, facts: next });
        setAck(false);
    }
    return (
        <AppModal
            open
            title={`${segment.spec.id} · 核对场景与提示词`}
            width={900}
            onCancel={() => !busy && onClose()}
            closable={!busy}
            footer={
                <div className="flex flex-wrap items-center justify-end gap-2">
                    <Checkbox checked={ack} disabled={busy} onChange={(e) => setAck(e.target.checked)}>
                        我已核对本段空间、描述和走位
                    </Checkbox>
                    <Button disabled={busy} onClick={onClose}>
                        取消
                    </Button>
                    <Button type="primary" loading={save.isPending} disabled={busy || !ack || !sceneId || (!textOnly && !image?.resourceId)} onClick={() => save.mutate()}>
                        保存场景与描述
                    </Button>
                </div>
            }
        >
            <div className="max-h-[66dvh] space-y-3 overflow-auto">
                {review.issues.length > 0 ? (
                    <Alert
                        type="warning"
                        showIcon
                        title="此段需要核对"
                        description={
                            <ul className="list-disc pl-5">
                                {review.issues.map((issue, i) => (
                                    <li key={i}>{issue}</li>
                                ))}
                            </ul>
                        }
                    />
                ) : null}
                {saveError ? <Alert type="error" showIcon title={saveError} /> : null}
                <div className="grid gap-4 md:grid-cols-2">
                    <section className="min-w-0 space-y-3">
                        <label className="block">
                            剧本场次
                            <Select
                                aria-label="剧本场次"
                                className="w-full"
                                disabled={busy}
                                value={sceneId || undefined}
                                options={(review.choices || []).map((choice) => ({ value: choice.sceneId, label: `${choice.sceneId} · ${choice.heading}` }))}
                                onChange={(value) => {
                                    setSceneId(value);
                                    setAck(false);
                                }}
                            />
                        </label>
                        <Checkbox
                            checked={textOnly}
                            disabled={busy}
                            onChange={(event) => {
                                setTextOnly(event.target.checked);
                                setAck(false);
                            }}
                        >
                            使用文字场景
                        </Checkbox>
                        {!textOnly ? (
                            <>
                                <label className="block">
                                    场景参考图
                                    <Select
                                        aria-label="场景参考图"
                                        className="w-full"
                                        disabled={busy}
                                        value={versionId || undefined}
                                        options={[...choices].map(([value, label]) => ({ value, label }))}
                                        onChange={(value) => {
                                            setVersionId(value);
                                            setDraft(undefined);
                                            setAck(false);
                                            setSaveError("");
                                        }}
                                    />
                                </label>
                                {candidate.isError ? <Alert type="error" title="场景参考图不可读，建议切换版本后再核对" /> : null}
                                {image?.resourceId ? (
                                    <>
                                        <Image src={resourceFileUrl(image.resourceId)} alt={`${image.label || "当前"}场景参考图`} className="max-h-52 w-full object-contain" />
                                        {!image.facts && !alignedDraft ? (
                                            <Button loading={extraction.isPending} disabled={busy} onClick={() => extraction.mutate()}>
                                                从当前图提取空间锚点
                                            </Button>
                                        ) : null}
                                        {extraction.error ? <Alert type="error" title={extraction.error.message} /> : null}
                                        {alignedDraft ? <p role="status">锚点草稿待确认。看不清的内容可放在“待核对”。</p> : image.facts ? <p>已核对本图的空间事实</p> : <p>此图尚未核对空间事实</p>}
                                        <label className="block">
                                            内外景
                                            <Select
                                                aria-label="图中内外景"
                                                className="w-full"
                                                value={facts.inOut || ""}
                                                disabled={busy}
                                                options={[
                                                    { value: "", label: "待核对" },
                                                    { value: "内", label: "室内" },
                                                    { value: "外", label: "室外" },
                                                    { value: "内外", label: "内外相通" },
                                                ]}
                                                onChange={(inOut) => setFacts({ ...facts, inOut })}
                                            />
                                        </label>
                                        <label className="block">
                                            固定布局
                                            <Input.TextArea disabled={busy} rows={2} maxLength={1500} value={facts.layout || ""} onChange={(e) => setFacts({ ...facts, layout: e.target.value })} />
                                        </label>
                                        {(
                                            [
                                                ["anchors", "已确认的锚点"],
                                                ["absent", "已确认不存在的结构"],
                                                ["unknown", "待核对"],
                                            ] as const
                                        ).map(([field, label]) => (
                                            <SceneFactList key={`${image.resourceId}-${field}`} label={label} value={facts[field] || []} disabled={busy} onChange={(value) => setFacts({ ...facts, [field]: value })} />
                                        ))}
                                    </>
                                ) : null}
                            </>
                        ) : null}
                    </section>
                    <section className="min-w-0 space-y-3">
                        <h3>本段场景描述</h3>
                        {Object.entries(sections).map(([name, text]) => (
                            <label key={name} className="block">
                                {name}
                                <Input.TextArea
                                    rows={3}
                                    disabled={busy}
                                    value={text}
                                    maxLength={2500}
                                    onChange={(e) => {
                                        setSections({ ...sections, [name]: e.target.value });
                                        setAck(false);
                                    }}
                                />
                                {text !== review.sections?.[name] ? (
                                    <details className="text-xs">
                                        <summary>查看修改前</summary>
                                        <p className="whitespace-pre-wrap">{review.sections?.[name]}</p>
                                    </details>
                                ) : null}
                            </label>
                        ))}
                        {segment.spec.verbatim ? (
                            <details>
                                <summary>本段保存时的导演稿</summary>
                                <p className="whitespace-pre-wrap text-sm">{segment.spec.verbatim}</p>
                                <p>生成时的原稿可在导演文档中查看。</p>
                            </details>
                        ) : null}
                        {shots.length ? (
                            <details className="space-y-2">
                                <summary>编辑相关镜头画面</summary>
                                {shots.map((text, i) => (
                                    <label key={i} className="block">
                                        第 {i + 1} 镜画面
                                        <Input.TextArea
                                            disabled={busy}
                                            rows={2}
                                            value={text}
                                            maxLength={2500}
                                            onChange={(e) => {
                                                setShots(shots.map((shot, n) => (n === i ? e.target.value : shot)));
                                                setAck(false);
                                            }}
                                        />
                                    </label>
                                ))}
                            </details>
                        ) : null}
                        <Button size="small" onClick={() => setShowCompiled(!showCompiled)}>
                            {showCompiled ? "收起出片提示词" : "查看当前出片提示词"}
                        </Button>
                        {showCompiled ? <p className="whitespace-pre-wrap text-sm">{compiled.data?.result.prompt || (compiled.isError ? "出片提示词暂不可用" : "正在读取…")}</p> : null}
                    </section>
                </div>
            </div>
        </AppModal>
    );
}

function SceneFactList({ label, value, disabled, onChange }: { label: string; value: string[]; disabled: boolean; onChange: (value: string[]) => void }) {
    const [editing, setEditing] = useState<string>();
    return (
        <label className="block">
            {label}
            <Input.TextArea
                disabled={disabled}
                rows={2}
                placeholder="每行一项"
                value={editing ?? value.join("\n")}
                onBlur={() => setEditing(undefined)}
                onChange={(event) => {
                    setEditing(event.target.value);
                    onChange(
                        event.target.value
                            .split("\n")
                            .map((item) => item.trim())
                            .filter(Boolean),
                    );
                }}
            />
        </label>
    );
}
