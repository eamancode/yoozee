import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, InputNumber, Modal } from "antd";
import { useBlocker } from "react-router";
import { Select } from "@/components/ui/base/select";
import { Archive, Check, FolderOpen, Image as ImageIcon, Lock, Save, ShieldAlert, Trash2 } from "lucide-react";

import { AssetLibraryPickerModal, type AssetLibraryPickerItem } from "@/components/assets/asset-library-picker-modal";
import { ModelPicker } from "@/components/model-picker";
import { projectVisualTypeLabel } from "@/lib/project-visual-type";
import { resourceFileUrl, resourceIdFromStorageKey } from "@/services/api/resources";
import { listProjectAssetsPage, updateProject } from "@/services/api/projects";
import { getDramaBrief, getDramaProgress, putDramaBrief, type DramaRunMode } from "@/services/api/drama";
import { uploadImage } from "@/services/image-storage";
import { useAssetStore } from "@/stores/use-asset-store";
import { modelDisplayName, useEffectiveConfig } from "@/stores/use-config-store";

import { sourceTypeLabel, type ProjectDetailViewProps } from "./shared";
import { projectLockedStyle } from "./chapter-style-lock";
import { DRAMA_RUN_MODES, DRAMA_STYLE_PROFILE_HINT, DRAMA_VIDEO_STOP_NOTE, SEGMENT_SECONDS_OPTIONS, dramaModelOptions, dramaStyleProfileOptions, findDramaModel, useDramaChannelFamilies, useDramaFamilyRatios, useDramaModels, useDramaStyleProfiles } from "../drama-model";
import { briefEpisodeSeconds, briefPrevizGate, briefSegmentSeconds, dramaBriefKey, EPISODE_SECONDS_RANGE, PREVIZ_GATE_OPTIONS, withEpisodeSeconds, withPrevizGate, withSegmentSeconds } from "./drama-brief";

export default function ProjectSettingsView({ detail, refreshProject }: ProjectDetailViewProps) {
    const { message, modal } = App.useApp();
    const effectiveConfig = useEffectiveConfig();
    const { project } = detail;
    const personalAssets = useAssetStore((state) => state.assets);
    const addAsset = useAssetStore((state) => state.addAsset);
    const [name, setName] = useState(project.name);
    const [description, setDescription] = useState(project.description || "");
    const [defaultImageModel, setDefaultImageModel] = useState(project.defaultImageModel || "");
    const [defaultVideoModel, setDefaultVideoModel] = useState(project.defaultVideoModel || "");
    const dramaModels = useDramaModels();
    const dramaFamilies = useDramaChannelFamilies(dramaModels.data);
    const dramaRatios = useDramaFamilyRatios(dramaModels.data);
    const [generationModel, setGenerationModel] = useState(project.generationModel || "");
    const savedRunMode: DramaRunMode = (DRAMA_RUN_MODES.find((item) => item.value === project.runMode) || DRAMA_RUN_MODES[0]).value;
    const [runMode, setRunMode] = useState<DramaRunMode>(savedRunMode);
    const [dramaProfile, setDramaProfile] = useState(project.styleProfile || "");
    const styleProfiles = useDramaStyleProfiles();
    const savedGenerationModel = project.generationModel || dramaModels.data?.default || "";
    const selectedDramaModel = findDramaModel(dramaModels.data, generationModel || savedGenerationModel);
    const dramaProgressQuery = useQuery({ queryKey: ["project", project.id, "drama", "progress"], queryFn: async ({ signal }) => (await getDramaProgress(project.id, signal)).progress });
    // 每集时长存在需求文档里（不是项目字段）：读需求文档，保存设置时一并写回。
    const queryClient = useQueryClient();
    const briefQuery = useQuery({ queryKey: dramaBriefKey(project.id), queryFn: async () => (await getDramaBrief(project.id)).document });
    const savedEpisodeSeconds = briefEpisodeSeconds(briefQuery.data) ?? null;
    const [episodeSeconds, setEpisodeSeconds] = useState<number | null>(savedEpisodeSeconds);
    useEffect(() => { setEpisodeSeconds(savedEpisodeSeconds); }, [savedEpisodeSeconds]);
    const episodeSecondsDirty = briefQuery.isSuccess && (episodeSeconds ?? null) !== savedEpisodeSeconds;
    // 单段最长秒数也在需求文档里，但经 updateProject 写（后端按生成模型一段的上限校验）。0 = 不限，按模型上限。
    const savedSegmentSeconds = briefSegmentSeconds(briefQuery.data) ?? 0;
    const [segmentSeconds, setSegmentSeconds] = useState(savedSegmentSeconds);
    useEffect(() => { setSegmentSeconds(savedSegmentSeconds); }, [savedSegmentSeconds]);
    const segmentSecondsDirty = briefQuery.isSuccess && segmentSeconds !== savedSegmentSeconds;
    // 预演闸门也在需求文档里（previz_gate），经 updateProject 写。
    const savedPrevizGate = briefPrevizGate(briefQuery.data);
    const [previzGate, setPrevizGate] = useState(savedPrevizGate);
    useEffect(() => { setPrevizGate(savedPrevizGate); }, [savedPrevizGate]);
    const previzGateDirty = briefQuery.isSuccess && previzGate !== savedPrevizGate;
    const segmentSecondsShrunk = segmentSecondsDirty && segmentSeconds > 0 && (savedSegmentSeconds === 0 || segmentSeconds < savedSegmentSeconds);
    const overLimitSegments = selectedDramaModel ? (dramaProgressQuery.data?.segments || []).filter((segment) => segment.durationMs > selectedDramaModel.duration.max * 1000) : [];
    const [archiveOpen, setArchiveOpen] = useState(false);
    const [coverPickerOpen, setCoverPickerOpen] = useState(false);
    const [coverPage, setCoverPage] = useState(1);
    const [coverPageSize, setCoverPageSize] = useState(40);
    const coverAssetsQuery = useQuery({
        queryKey: ["project", project.id, "assets", "cover-picker", coverPage, coverPageSize],
        queryFn: () => listProjectAssetsPage(project.id, { page: coverPage, pageSize: coverPageSize, mediaType: "image" }),
        enabled: coverPickerOpen,
    });
    const projectCoverAssets = coverAssetsQuery.data?.assets || [];
    useEffect(() => { setName(project.name); setDescription(project.description || ""); setDefaultImageModel(project.defaultImageModel || ""); setDefaultVideoModel(project.defaultVideoModel || ""); setGenerationModel(project.generationModel || ""); setRunMode(savedRunMode); setDramaProfile(project.styleProfile || ""); }, [project, savedRunMode]);
    const dirty = useMemo(() => name.trim() !== project.name || description !== (project.description || "") || defaultImageModel !== (project.defaultImageModel || "") || defaultVideoModel !== (project.defaultVideoModel || "") || (generationModel !== "" && generationModel !== savedGenerationModel) || runMode !== savedRunMode || dramaProfile !== (project.styleProfile || "") || episodeSecondsDirty || segmentSecondsDirty || previzGateDirty, [runMode, savedRunMode, dramaProfile, defaultImageModel, defaultVideoModel, description, episodeSecondsDirty, generationModel, name, previzGateDirty, project, savedGenerationModel, segmentSecondsDirty]);
    const lockedStyle = useMemo(() => projectLockedStyle(project.bibleJson), [project.bibleJson]);
    const coverPickerItems = useMemo<AssetLibraryPickerItem[]>(() => {
        const result: AssetLibraryPickerItem[] = [];
        const seenResourceIds = new Set<string>();
        for (const asset of personalAssets) {
            if (asset.kind !== "image") continue;
            const resourceId = resourceIdFromStorageKey(asset.data.storageKey);
            if (!resourceId || seenResourceIds.has(resourceId)) continue;
            seenResourceIds.add(resourceId);
            result.push({ id: asset.id, title: asset.title, category: "image", kindLabel: "图片", asset, description: asset.note || "个人素材库", searchText: (asset.tags || []).join(" ") });
        }
        for (const asset of projectCoverAssets) {
            if (asset.mediaType !== "image") continue;
            const resourceId = resourceIdFromStorageKey(asset.storageKey);
            if (!resourceId || seenResourceIds.has(resourceId)) continue;
            seenResourceIds.add(resourceId);
            result.push({ id: `project:${asset.id}`, title: asset.title, category: "image", kindLabel: "项目图片", imageUrl: resourceFileUrl(resourceId), description: "项目素材库", searchText: asset.title });
        }
        if (project.coverResourceId && !seenResourceIds.has(project.coverResourceId)) {
            result.unshift({ id: `current:${project.coverResourceId}`, title: "当前项目主图", category: "image", kindLabel: "当前主图", imageUrl: resourceFileUrl(project.coverResourceId) });
        }
        return result;
    }, [personalAssets, project.coverResourceId, projectCoverAssets]);
    const coverResourceByItemId = useMemo(() => new Map(coverPickerItems.flatMap((item) => {
        if (item.id.startsWith("current:")) return [[item.id, item.id.slice("current:".length)] as const];
        if (item.id.startsWith("project:")) {
            const asset = projectCoverAssets.find((entry) => `project:${entry.id}` === item.id);
            const resourceId = resourceIdFromStorageKey(asset?.storageKey);
            return resourceId ? [[item.id, resourceId] as const] : [];
        }
        const asset = personalAssets.find((entry) => entry.id === item.id);
        const resourceId = asset?.kind === "image" ? resourceIdFromStorageKey(asset.data.storageKey) : "";
        return resourceId ? [[item.id, resourceId] as const] : [];
    })), [coverPickerItems, personalAssets, projectCoverAssets]);
    const coverResourceByItemIdRef = useRef(new Map<string, string>());
    useEffect(() => {
        for (const [itemId, resourceId] of coverResourceByItemId) coverResourceByItemIdRef.current.set(itemId, resourceId);
    }, [coverResourceByItemId]);
    const currentCoverItemId = coverPickerItems.find((item) => coverResourceByItemId.get(item.id) === project.coverResourceId)?.id;
    const saveMutation = useMutation({
        mutationFn: async () => {
            await updateProject(project.id, { name: name.trim(), description, defaultImageModel, defaultVideoModel, runMode, ...(generationModel ? { generationModel } : {}), ...(dramaProfile ? { styleProfile: dramaProfile } : {}), ...(segmentSecondsDirty ? { segmentSeconds } : {}), ...(previzGateDirty ? { previzGate } : {}) });
            // 项目字段先存：需求文档写回时不带生成模型和生成模式，不会把刚存的项目字段改回去；单段秒数、预演闸门已随项目写入，这里带上新值。
            if (episodeSecondsDirty && briefQuery.data) {
                const { document } = await putDramaBrief(project.id, withEpisodeSeconds(withPrevizGate(withSegmentSeconds(briefQuery.data, segmentSeconds), previzGate), episodeSeconds));
                queryClient.setQueryData(dramaBriefKey(project.id), document);
            } else if (segmentSecondsDirty || previzGateDirty) {
                await queryClient.invalidateQueries({ queryKey: dramaBriefKey(project.id) });
            }
        },
        onSuccess: () => { refreshProject(); void dramaProgressQuery.refetch(); message.success("项目设置已保存"); },
        onError: (error) => message.error(error instanceof Error ? error.message : "项目设置保存失败"),
    });
    // 改了模型、生成模式却没点「保存设置」就离开，改动会静默丢失；路由跳转和关闭标签页都要先问一句。
    const unsaved = dirty && !saveMutation.isPending;
    const blocker = useBlocker(unsaved);
    const leaveConfirmOpenRef = useRef(false);
    useEffect(() => {
        const beforeUnload = (event: BeforeUnloadEvent) => {
            if (unsaved) event.preventDefault();
        };
        window.addEventListener("beforeunload", beforeUnload);
        return () => window.removeEventListener("beforeunload", beforeUnload);
    }, [unsaved]);
    useEffect(() => {
        if (blocker.state !== "blocked" || leaveConfirmOpenRef.current) return;
        leaveConfirmOpenRef.current = true;
        modal.confirm({
            title: "项目设置还没保存",
            content: "模型、生成模式或基础信息的修改还没有保存，离开后会丢失。",
            okText: "放弃修改并离开",
            okButtonProps: { danger: true },
            cancelText: "留下保存",
            onOk: () => { leaveConfirmOpenRef.current = false; blocker.proceed(); },
            onCancel: () => { leaveConfirmOpenRef.current = false; blocker.reset(); },
        });
    }, [blocker, modal]);
    const archiveMutation = useMutation({ mutationFn: () => updateProject(project.id, { status: project.status === "archived" ? "active" : "archived" }), onSuccess: () => { setArchiveOpen(false); refreshProject(); message.success(project.status === "archived" ? "项目已恢复" : "项目已归档"); }, onError: (error) => message.error(error instanceof Error ? error.message : "项目状态更新失败") });
    const coverMutation = useMutation({ mutationFn: (coverResourceId: string) => updateProject(project.id, { coverResourceId }), onSuccess: (_, coverResourceId) => { setCoverPickerOpen(false); refreshProject(); message.success(coverResourceId ? "项目主图已更新" : "项目主图已移除"); }, onError: (error) => message.error(error instanceof Error ? error.message : "项目主图更新失败") });

    return (
        <div>
            <header className="flex items-end justify-between gap-3 pb-3"><div><h2 className="text-lg font-semibold">项目设置</h2><p className="mt-1 text-xs text-foreground/48">基础信息、生成模式与归档管理</p></div><Button type={dirty ? "primary" : "default"} icon={dirty ? <Save className="size-3.5" /> : <Check className="size-3.5" />} disabled={!dirty || !name.trim()} loading={saveMutation.isPending} onClick={() => saveMutation.mutate()}>{dirty ? "保存设置" : "已保存"}</Button></header>

            <section className="py-5">
                <h3 className="mb-3 text-sm font-semibold">基础设置</h3>
                <div className="grid gap-x-4 gap-y-3 md:grid-cols-2 xl:grid-cols-4">
                    <Field label="项目名称" className="xl:col-span-2"><Input value={name} onChange={(event) => setName(event.target.value)} /></Field>
                    <ReadOnlyField label="画幅" value={project.aspectRatio} note="建项后不可改" />
                    <ReadOnlyField label="内容来源" value={sourceTypeLabel(project.sourceType)} />
                    <Field label="项目简介" className="md:col-span-2 xl:col-span-4"><Input value={description} onChange={(event) => setDescription(event.target.value)} placeholder="一句话说明项目目标" /></Field>
                </div>
            </section>

            <section className="border-t border-border/70 py-5">
                <div className="mb-3"><h3 className="text-sm font-semibold">短剧生成</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">一段 = 一次视频生成。生成模型决定一段最长几秒和提示词方言；生成模式决定一步做完后要不要自动接着做下一步。</p></div>
                <div className="grid gap-4 md:grid-cols-2">
                    <Field label="生成模型"><Select className="w-full" loading={dramaModels.isLoading} value={generationModel || savedGenerationModel || undefined} options={dramaModelOptions(dramaModels.data, dramaFamilies, { aspectRatio: project.aspectRatio, ratios: dramaRatios })} onChange={(value) => { setGenerationModel(value); const max = findDramaModel(dramaModels.data, value)?.duration.max; if (max && segmentSeconds > max) setSegmentSeconds(SEGMENT_SECONDS_OPTIONS.filter((item) => item <= max).at(-1) ?? 0); }} /></Field>
                    <Field label="生成模式"><Select className="w-full" value={runMode} options={DRAMA_RUN_MODES.map(({ label, value }) => ({ label, value }))} onChange={setRunMode} /></Field>
                    <Field label="风格档案"><Select className="w-full" loading={styleProfiles.isLoading} value={dramaProfile || undefined} options={dramaStyleProfileOptions(styleProfiles.data)} onChange={setDramaProfile} /></Field>
                    <Field label="单段最长时长"><Select className="w-full" value={segmentSeconds} disabled={!briefQuery.isSuccess} options={[{ label: `不限（按模型上限 ${selectedDramaModel?.duration.max ?? "-"} 秒）`, value: 0 }, ...SEGMENT_SECONDS_OPTIONS.map((value) => ({ label: `${value} 秒`, value, disabled: Boolean(selectedDramaModel && value > selectedDramaModel.duration.max) }))]} onChange={setSegmentSeconds} /></Field>
                    <Field label="视频前预演闸门"><Select className="w-full" value={previzGate} disabled={!briefQuery.isSuccess} options={PREVIZ_GATE_OPTIONS} onChange={setPrevizGate} /></Field>
                    <Field label="每集时长（秒）"><InputNumber className="w-full" min={EPISODE_SECONDS_RANGE.min} max={EPISODE_SECONDS_RANGE.max} step={10} precision={0} value={episodeSeconds} disabled={!briefQuery.isSuccess} placeholder="不限" onChange={(value) => setEpisodeSeconds(typeof value === "number" ? value : null)} /></Field>
                </div>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">{DRAMA_STYLE_PROFILE_HINT}</p>
                {segmentSecondsShrunk ? <div className="mt-2 rounded-md bg-amber-500/5 px-3 py-2 text-[var(--fs-label)] leading-5 text-amber-600 dark:text-amber-400">单段最长时长改小后，下次装段生效；已经装好的段不会自动重拆，可在章节页「进度」侧栏里点「重新装段」。</div> : null}
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">单段最长时长只能比生成模型一段的上限更严，装段时按两者里小的那个切段。</p>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">预演闸门开着时，每一段要先生成并选中黑白动作预演，才能生成视频（制作台和 Agent 都按这个拦）；跟生成模式时，跑到出视频前关，另两档开。</p>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">每集时长写进生成分镜的提示词（镜头估时合计不超过它的 1.1 倍），保存分镜时按它检查分段合计；不填就不限。</p>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">{DRAMA_RUN_MODES.find((item) => item.value === runMode)?.hint}。改生成模式只影响之后的接续，已经停住的环节不会自动补做。{DRAMA_VIDEO_STOP_NOTE}</p>
                {selectedDramaModel && dramaFamilies && !dramaFamilies.has(selectedDramaModel.id) ? <div className="mt-2 rounded-md bg-amber-500/5 px-3 py-2 text-[var(--fs-label)] leading-5 text-amber-600 dark:text-amber-400">当前没有 {selectedDramaModel.label} 的视频渠道，分镜制作页无法按这个模型生成视频。请先配置渠道，或改用已有渠道的模型。</div> : null}
                {overLimitSegments.length ? <div className="mt-2 rounded-md bg-amber-500/5 px-3 py-2 text-[var(--fs-label)] leading-5 text-amber-600 dark:text-amber-400">{overLimitSegments.length} 段超过 {selectedDramaModel?.label} 一段 {selectedDramaModel?.duration.max} 秒的上限（{overLimitSegments.slice(0, 6).map((segment) => segment.segmentId || segment.title).join("、")}{overLimitSegments.length > 6 ? " 等" : ""}）。保存后这些段不能生成视频，需要先拆分。</div> : null}
            </section>

            <section className="border-t border-border/70 py-5">
                <div className="mb-3"><h3 className="text-sm font-semibold">默认生成模型</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">分镜图、动作预演与镜头视频优先使用项目默认；未设置或模型不可用时跟随工作台全局默认。</p></div>
                <div className="grid gap-4 md:grid-cols-2">
                    <Field label="默认生图模型">
                        <div className="flex items-center gap-2">
                            <ModelPicker config={effectiveConfig} value={defaultImageModel} capability="image" onChange={setDefaultImageModel} fullWidth placeholder={`跟随全局 · ${modelDisplayName(effectiveConfig, effectiveConfig.imageModel) || "未配置"}`} showSelectedPrice />
                            {defaultImageModel ? <Button type="text" size="small" onClick={() => setDefaultImageModel("")}>跟随全局</Button> : null}
                        </div>
                    </Field>
                    <Field label="默认视频模型">
                        <div className="flex items-center gap-2">
                            <ModelPicker config={effectiveConfig} value={defaultVideoModel} capability="video" onChange={setDefaultVideoModel} fullWidth placeholder={`跟随全局 · ${modelDisplayName(effectiveConfig, effectiveConfig.videoModel) || "未配置"}`} showSelectedPrice />
                            {defaultVideoModel ? <Button type="text" size="small" onClick={() => setDefaultVideoModel("")}>跟随全局</Button> : null}
                        </div>
                    </Field>
                </div>
            </section>

            <section className="border-t border-border/70 py-5">
                <div className="mb-3 flex items-center justify-between gap-3"><div><h3 className="text-sm font-semibold">项目主图</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">项目列表优先展示这张图片；未设置时显示占位图</p></div><span className="text-[var(--fs-label)] text-foreground/40">建议使用与项目画幅一致的图片</span></div>
                <div className="flex flex-col gap-3 rounded-lg border border-border/70 bg-surface-active p-3 sm:flex-row sm:items-center">
                    {project.coverResourceId ? <img src={resourceFileUrl(project.coverResourceId)} alt={`${project.name}项目主图`} className="aspect-video w-full shrink-0 rounded-md bg-foreground/5 object-cover sm:w-52" /> : <span className="grid aspect-video w-full shrink-0 place-items-center rounded-md bg-foreground/5 text-foreground/30 sm:w-52"><ImageIcon className="size-6" /></span>}
                    <div className="min-w-0 flex-1"><div className="text-sm font-medium">{project.coverResourceId ? "已设置项目主图" : "尚未设置项目主图"}</div><p className="mt-1 text-xs leading-5 text-foreground/48">从个人素材库或项目素材库选择，也可以在选择窗口中上传一张新图片。</p></div>
                    <div className="flex shrink-0 gap-2"><Button icon={<FolderOpen className="size-3.5" />} onClick={() => { setCoverPage(1); setCoverPickerOpen(true); }}>{project.coverResourceId ? "替换主图" : "设置主图"}</Button>{project.coverResourceId ? <Button danger type="text" icon={<Trash2 className="size-3.5" />} loading={coverMutation.isPending} onClick={() => coverMutation.mutate("")}>移除</Button> : null}</div>
                </div>
            </section>

            <section className="border-t border-border/70 py-5">
                <div className="mb-3"><h3 className="text-sm font-semibold">画面类型与画风</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">画面类型建项时定，之后不可改；画风由导演定画风或从风格中心挑，锁定后风格锚和主基调色卡写进项目。</p></div>
                <div className="grid gap-4 md:grid-cols-2">
                    <ReadOnlyField label="画面类型" value={projectVisualTypeLabel(project.visualType) || "未设置"} note="建项后不可改" />
                    <ReadOnlyField label="画风" value={lockedStyle.locked ? (lockedStyle.record?.source === "library" ? "取自风格中心，已锁定" : "导演分析后锁定") : project.style?.mode === "library" ? "从风格中心挑（还没锁定）" : "交给导演分析（拆完剧本后自动定）"} icon={lockedStyle.locked ? <Lock className="size-3.5" /> : undefined} />
                </div>
            </section>

            <section className="py-4">
                <div className="flex flex-col gap-3 rounded-lg bg-red-500/5 px-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                    <div className="flex min-w-0 items-center gap-2.5"><span className="grid size-7 shrink-0 place-items-center rounded bg-red-500/10 text-red-500"><Archive className="size-3.5" /></span><div className="min-w-0"><h3 className="text-sm font-medium">{project.status === "archived" ? "恢复项目" : "归档项目"}</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/48">{project.status === "archived" ? "恢复后可继续创建章节、画布和生成任务" : "保留全部章节、画布和资产，停止项目内新建与生成"}</p></div></div>
                    <Button size="small" danger={project.status !== "archived"} icon={project.status === "archived" ? <Check className="size-3.5" /> : <ShieldAlert className="size-3.5" />} onClick={() => setArchiveOpen(true)}>{project.status === "archived" ? "恢复项目" : "归档项目"}</Button>
                </div>
            </section>

            <Modal className="workspace-modal workspace-modal-compact" title={project.status === "archived" ? "恢复项目" : "归档项目"} open={archiveOpen} okText={project.status === "archived" ? "确认恢复" : "确认归档"} cancelText="取消" okButtonProps={{ danger: project.status !== "archived", loading: archiveMutation.isPending }} onCancel={() => setArchiveOpen(false)} onOk={() => archiveMutation.mutate()} styles={{ body: { paddingTop: 12 } }}><p className="m-0 text-sm leading-6 text-foreground/65">{project.status === "archived" ? "恢复后项目会重新进入可编辑状态。" : "归档不会删除章节、画布或资产，画布文档仍可在创作画布中打开。"}</p></Modal>
            <AssetLibraryPickerModal
                open={coverPickerOpen}
                items={coverPickerItems}
                mediaKinds={["image"]}
                categoryLabels={{ all: "全部图片", image: "图片" }}
                initialCategory="image"
                initialSelectedIds={currentCoverItemId ? [currentCoverItemId] : []}
                multiple={false}
                eyebrow="项目设置"
                title="选择项目主图"
                confirmLabel={() => "设为项目主图"}
                emptyTitle="素材库还没有图片"
                emptyDescription="可以从底部上传一张新图片，上传后会自动选中。"
                loading={coverAssetsQuery.isLoading}
                pagination={{ current: coverPage, pageSize: coverPageSize, total: coverAssetsQuery.data?.total || 0, onChange: (nextPage, nextPageSize) => { setCoverPage(nextPageSize !== coverPageSize ? 1 : nextPage); setCoverPageSize(nextPageSize); } }}
                upload={{ accept: "image/png,image/jpeg,image/webp,image/avif", description: "支持 PNG、JPG、WebP、AVIF", onUpload: async (files) => {
                    const file = Array.from(files)[0];
                    if (!file?.type.startsWith("image/")) throw new Error("请选择图片文件");
                    const uploaded = await uploadImage(file);
                    const resourceId = resourceIdFromStorageKey(uploaded.storageKey);
                    if (!resourceId) throw new Error("图片上传未同步到服务端资源库，请检查后端连接");
                    const id = addAsset({ kind: "image", title: file.name.replace(/\.[^.]+$/, "") || "项目主图", coverUrl: uploaded.url, tags: ["项目主图"], status: "confirmed", source: "项目设置", data: { dataUrl: uploaded.url, storageKey: uploaded.storageKey, width: uploaded.width, height: uploaded.height, bytes: uploaded.bytes, mimeType: uploaded.mimeType } });
                    return [id];
                } }}
                onClose={() => setCoverPickerOpen(false)}
                onConfirm={async (ids) => {
                    const id = ids[0];
                    let resourceId = coverResourceByItemIdRef.current.get(id);
                    if (!resourceId) {
                        const asset = useAssetStore.getState().assets.find((entry) => entry.id === id);
                        resourceId = asset?.kind === "image" ? resourceIdFromStorageKey(asset.data.storageKey) : "";
                    }
                    if (!resourceId) throw new Error("所选图片尚未同步到服务端资源库");
                    await coverMutation.mutateAsync(resourceId);
                }}
            />
        </div>
    );

}

function Field({ label, className = "", children }: { label: string; className?: string; children: ReactNode }) {
    return <label className={`grid gap-1.5 text-xs ${className}`}><span className="font-medium text-foreground/62">{label}</span>{children}</label>;
}

function ReadOnlyField({ label, value, note, icon }: { label: string; value: string; note?: string; icon?: ReactNode }) {
    return <div className="grid gap-1.5 text-xs"><span className="font-medium text-foreground/62">{label}{note ? <span className="ml-1.5 font-normal text-foreground/38">{note}</span> : null}</span><span className="flex min-h-8 items-center gap-1.5 rounded-md bg-surface-active px-3 text-foreground/75">{icon}{value}</span></div>;
}
