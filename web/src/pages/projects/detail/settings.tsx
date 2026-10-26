import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, InputNumber, Modal } from "antd";
import { useBlocker } from "react-router";
import { Select } from "@/components/ui/base/select";
import { Archive, Check, Eye, FolderOpen, Image as ImageIcon, Palette, Pencil, Save, ShieldAlert, Trash2 } from "lucide-react";

import { AssetLibraryPickerModal, type AssetLibraryPickerItem } from "@/components/assets/asset-library-picker-modal";
import { CanvasStyleDetailModal, CanvasStylePickerModal, resolveProjectCanvasStyle, type CanvasStylePreset } from "@/components/canvas/canvas-style-picker-modal";
import { ModelPicker } from "@/components/model-picker";
import { createStyleProfileSnapshot, parseStyleProfile, resolveStyleExecutionPlan, serializeStyleProfile } from "@/lib/canvas/style-profile";
import { resourceFileUrl, resourceIdFromStorageKey } from "@/services/api/resources";
import { listProjectAssetsPage, updateProject } from "@/services/api/projects";
import { getDramaBrief, getDramaProgress, putDramaBrief, type DramaAuthMode } from "@/services/api/drama";
import { uploadImage } from "@/services/image-storage";
import { useAssetStore } from "@/stores/use-asset-store";
import { modelDisplayName, resolveModelRequestConfig, useEffectiveConfig } from "@/stores/use-config-store";

import type { ProjectDetailViewProps } from "./shared";
import { DRAMA_AUTH_OPTIONS, DRAMA_STYLE_PROFILE_HINT, SEGMENT_SECONDS_OPTIONS, dramaModelOptions, dramaStyleProfileOptions, findDramaModel, recommendedDramaStyleProfile, useDramaChannelFamilies, useDramaFamilyRatios, useDramaModels, useDramaStyleProfiles } from "../drama-model";
import { briefEpisodeSeconds, briefSegmentSeconds, dramaBriefKey, EPISODE_SECONDS_RANGE, withEpisodeSeconds, withSegmentSeconds } from "./drama-brief";

export default function ProjectSettingsView({ detail, refreshProject }: ProjectDetailViewProps) {
    const { message, modal } = App.useApp();
    const effectiveConfig = useEffectiveConfig();
    const { project } = detail;
    const personalAssets = useAssetStore((state) => state.assets);
    const addAsset = useAssetStore((state) => state.addAsset);
    const [name, setName] = useState(project.name);
    const [description, setDescription] = useState(project.description || "");
    const [aspectRatio, setAspectRatio] = useState(project.aspectRatio);
    const [sourceType, setSourceType] = useState(project.sourceType);
    const [stylePresetId, setStylePresetId] = useState(project.stylePresetId || "");
    const [styleProfileJson, setStyleProfileJson] = useState(project.styleProfileJson || "");
    const [defaultImageModel, setDefaultImageModel] = useState(project.defaultImageModel || "");
    const [defaultVideoModel, setDefaultVideoModel] = useState(project.defaultVideoModel || "");
    const dramaModels = useDramaModels();
    const dramaFamilies = useDramaChannelFamilies(dramaModels.data);
    const dramaRatios = useDramaFamilyRatios(dramaModels.data);
    const [generationModel, setGenerationModel] = useState(project.generationModel || "");
    const [authMode, setAuthMode] = useState<DramaAuthMode>(project.authMode === "full" ? "full" : "step");
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
    const segmentSecondsShrunk = segmentSecondsDirty && segmentSeconds > 0 && (savedSegmentSeconds === 0 || segmentSeconds < savedSegmentSeconds);
    const overLimitSegments = selectedDramaModel ? (dramaProgressQuery.data?.segments || []).filter((segment) => segment.durationMs > selectedDramaModel.duration.max * 1000) : [];
    const [styleDetail, setStyleDetail] = useState<CanvasStylePreset | null>(null);
    const [stylePickerOpen, setStylePickerOpen] = useState(false);
    const [styleEditorRequested, setStyleEditorRequested] = useState(false);
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
    useEffect(() => { setName(project.name); setDescription(project.description || ""); setAspectRatio(project.aspectRatio); setSourceType(project.sourceType); setStylePresetId(project.stylePresetId || ""); setStyleProfileJson(project.styleProfileJson || ""); setDefaultImageModel(project.defaultImageModel || ""); setDefaultVideoModel(project.defaultVideoModel || ""); setGenerationModel(project.generationModel || ""); setAuthMode(project.authMode === "full" ? "full" : "step"); setDramaProfile(project.styleProfile || ""); }, [project]);
    const dirty = useMemo(() => name.trim() !== project.name || description !== (project.description || "") || aspectRatio !== project.aspectRatio || sourceType !== project.sourceType || stylePresetId !== (project.stylePresetId || "") || styleProfileJson !== (project.styleProfileJson || "") || defaultImageModel !== (project.defaultImageModel || "") || defaultVideoModel !== (project.defaultVideoModel || "") || (generationModel !== "" && generationModel !== savedGenerationModel) || authMode !== (project.authMode === "full" ? "full" : "step") || dramaProfile !== (project.styleProfile || "") || episodeSecondsDirty || segmentSecondsDirty, [aspectRatio, authMode, dramaProfile, defaultImageModel, defaultVideoModel, description, episodeSecondsDirty, generationModel, name, project, savedGenerationModel, segmentSecondsDirty, sourceType, stylePresetId, styleProfileJson]);
    const selectedStyle = useMemo(() => resolveProjectCanvasStyle(stylePresetId, styleProfileJson), [stylePresetId, styleProfileJson]);
    const styleProfile = useMemo(() => parseStyleProfile(styleProfileJson) || selectedStyle?.profile || (selectedStyle ? createStyleProfileSnapshot(selectedStyle) : null), [selectedStyle, styleProfileJson]);
    const enabledStyleAssets = styleProfile?.assets.filter((asset) => asset.enabled !== false) || [];
    const styleExecutionPlans = useMemo(() => {
        if (!styleProfile) return null;
        const imageConfig = resolveModelRequestConfig(effectiveConfig, effectiveConfig.imageModel || effectiveConfig.model);
        const videoConfig = resolveModelRequestConfig(effectiveConfig, effectiveConfig.videoModel || effectiveConfig.model);
        return {
            image: resolveStyleExecutionPlan(styleProfile, { mode: "image", model: imageConfig.model, interfaceType: imageConfig.interfaceType || imageConfig.apiFormat }),
            video: resolveStyleExecutionPlan(styleProfile, { mode: "video", model: videoConfig.model, interfaceType: videoConfig.interfaceType || videoConfig.apiFormat }),
        };
    }, [effectiveConfig, styleProfile]);
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
            await updateProject(project.id, { name: name.trim(), description, aspectRatio, sourceType, stylePresetId, styleProfileJson, defaultImageModel, defaultVideoModel, authMode, ...(generationModel ? { generationModel } : {}), ...(dramaProfile ? { styleProfile: dramaProfile } : {}), ...(segmentSecondsDirty ? { segmentSeconds } : {}) });
            // 项目字段先存：需求文档写回时不带生成模型和授权档位，不会把刚存的项目字段改回去；单段秒数已随项目写入，这里带上新值。
            if (episodeSecondsDirty && briefQuery.data) {
                const { document } = await putDramaBrief(project.id, withEpisodeSeconds(withSegmentSeconds(briefQuery.data, segmentSeconds), episodeSeconds));
                queryClient.setQueryData(dramaBriefKey(project.id), document);
            } else if (segmentSecondsDirty) {
                await queryClient.invalidateQueries({ queryKey: dramaBriefKey(project.id) });
            }
        },
        onSuccess: () => { refreshProject(); void dramaProgressQuery.refetch(); message.success("项目设置已保存"); },
        onError: (error) => message.error(error instanceof Error ? error.message : "项目设置保存失败"),
    });
    // 选了画风、改了模型却没点「保存设置」就离开，改动会静默丢失；路由跳转和关闭标签页都要先问一句。
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
            content: "画风、模型或基础信息的修改还没有保存，离开后会丢失。",
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
            <header className="flex items-end justify-between gap-3 pb-3"><div><h2 className="text-lg font-semibold">项目设置</h2><p className="mt-1 text-xs text-foreground/48">基础信息、项目画风与归档管理</p></div><Button type={dirty ? "primary" : "default"} icon={dirty ? <Save className="size-3.5" /> : <Check className="size-3.5" />} disabled={!dirty || !name.trim()} loading={saveMutation.isPending} onClick={() => saveMutation.mutate()}>{dirty ? "保存设置" : "已保存"}</Button></header>

            <section className="py-5">
                <h3 className="mb-3 text-sm font-semibold">基础设置</h3>
                <div className="grid gap-x-4 gap-y-3 md:grid-cols-2 xl:grid-cols-4">
                    <Field label="项目名称" className="xl:col-span-2"><Input value={name} onChange={(event) => setName(event.target.value)} /></Field>
                    <Field label="默认画幅"><Select className="w-full" value={aspectRatio} options={[{ label: "9:16 · 竖屏短剧", value: "9:16" }, { label: "16:9 · 横屏", value: "16:9" }, { label: "1:1 · 方形", value: "1:1" }, { label: "4:3 · 标准横屏", value: "4:3" }]} onChange={setAspectRatio} /></Field>
                    <Field label="内容来源"><Select className="w-full" value={sourceType} options={[{ label: "空白开始", value: "blank" }, { label: "导入小说", value: "novel" }, { label: "粘贴文本", value: "text" }]} onChange={setSourceType} /></Field>
                    <Field label="项目简介" className="md:col-span-2 xl:col-span-4"><Input value={description} onChange={(event) => setDescription(event.target.value)} placeholder="一句话说明项目目标" /></Field>
                </div>
            </section>

            <section className="border-t border-border/70 py-5">
                <div className="mb-3"><h3 className="text-sm font-semibold">短剧生成</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">一段 = 一次视频生成。生成模型决定一段最长几秒和提示词方言；授权档位决定 Agent 每个节点是否停下来等你确认。</p></div>
                <div className="grid gap-4 md:grid-cols-2">
                    <Field label="生成模型"><Select className="w-full" loading={dramaModels.isLoading} value={generationModel || savedGenerationModel || undefined} options={dramaModelOptions(dramaModels.data, dramaFamilies, { aspectRatio, ratios: dramaRatios })} onChange={(value) => { setGenerationModel(value); const max = findDramaModel(dramaModels.data, value)?.duration.max; if (max && segmentSeconds > max) setSegmentSeconds(SEGMENT_SECONDS_OPTIONS.filter((item) => item <= max).at(-1) ?? 0); }} /></Field>
                    <Field label="授权档位"><Select className="w-full" value={authMode} options={DRAMA_AUTH_OPTIONS.map(({ label, value }) => ({ label, value }))} onChange={setAuthMode} /></Field>
                    <Field label="风格档案"><Select className="w-full" loading={styleProfiles.isLoading} value={dramaProfile || undefined} options={dramaStyleProfileOptions(styleProfiles.data, recommendedDramaStyleProfile(styleProfiles.data, styleProfile || undefined))} onChange={setDramaProfile} /></Field>
                    <Field label="单段最长时长"><Select className="w-full" value={segmentSeconds} disabled={!briefQuery.isSuccess} options={[{ label: `不限（按模型上限 ${selectedDramaModel?.duration.max ?? "-"} 秒）`, value: 0 }, ...SEGMENT_SECONDS_OPTIONS.map((value) => ({ label: `${value} 秒`, value, disabled: Boolean(selectedDramaModel && value > selectedDramaModel.duration.max) }))]} onChange={setSegmentSeconds} /></Field>
                    <Field label="每集时长（秒）"><InputNumber className="w-full" min={EPISODE_SECONDS_RANGE.min} max={EPISODE_SECONDS_RANGE.max} step={10} precision={0} value={episodeSeconds} disabled={!briefQuery.isSuccess} placeholder="不限" onChange={(value) => setEpisodeSeconds(typeof value === "number" ? value : null)} /></Field>
                </div>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">{DRAMA_STYLE_PROFILE_HINT}换了画风不会自动改档案。</p>
                {segmentSecondsShrunk ? <div className="mt-2 rounded-md bg-amber-500/5 px-3 py-2 text-[var(--fs-label)] leading-5 text-amber-600 dark:text-amber-400">单段最长时长改小后，下次装段生效；已经装好的段不会自动重拆，可在章节页「进度」侧栏里点「重新装段」。</div> : null}
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">单段最长时长只能比生成模型一段的上限更严，装段时按两者里小的那个切段。</p>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">每集时长写进生成分镜的提示词（镜头估时合计不超过它的 1.1 倍），保存分镜时按它检查分段合计；不填就不限。</p>
                <p className="mt-2 text-[var(--fs-label)] leading-5 text-foreground/48">{DRAMA_AUTH_OPTIONS.find((item) => item.value === authMode)?.hint}{selectedDramaModel && !selectedDramaModel.calibrated ? "。该模型档案尚未用真实样片校准，首次使用请先跑一次校准。" : ""}</p>
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
                <div className="mb-3 flex items-center justify-between gap-3"><div><h3 className="text-sm font-semibold">项目主图</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">项目列表优先展示这张图片；未设置时使用项目画风示意图</p></div><span className="text-[var(--fs-label)] text-foreground/40">建议使用与项目画幅一致的图片</span></div>
                <div className="flex flex-col gap-3 rounded-lg border border-border/70 bg-surface-active p-3 sm:flex-row sm:items-center">
                    {project.coverResourceId ? <img src={resourceFileUrl(project.coverResourceId)} alt={`${project.name}项目主图`} className="aspect-video w-full shrink-0 rounded-md bg-foreground/5 object-cover sm:w-52" /> : <span className="grid aspect-video w-full shrink-0 place-items-center rounded-md bg-foreground/5 text-foreground/30 sm:w-52"><ImageIcon className="size-6" /></span>}
                    <div className="min-w-0 flex-1"><div className="text-sm font-medium">{project.coverResourceId ? "已设置项目主图" : "尚未设置项目主图"}</div><p className="mt-1 text-xs leading-5 text-foreground/48">从个人素材库或项目素材库选择，也可以在选择窗口中上传一张新图片。</p></div>
                    <div className="flex shrink-0 gap-2"><Button icon={<FolderOpen className="size-3.5" />} onClick={() => { setCoverPage(1); setCoverPickerOpen(true); }}>{project.coverResourceId ? "替换主图" : "设置主图"}</Button>{project.coverResourceId ? <Button danger type="text" icon={<Trash2 className="size-3.5" />} loading={coverMutation.isPending} onClick={() => coverMutation.mutate("")}>移除</Button> : null}</div>
                </div>
            </section>

            <section className="border-t border-border/70 py-5">
                <div className="mb-3 flex items-center justify-between gap-3"><div><h3 className="text-sm font-semibold">项目画风</h3><p className="mt-0.5 text-[var(--fs-label)] text-foreground/45">项目保存当前版本快照；修改“我的风格”不会自动改写历史项目</p></div>{styleProfile ? <span className="text-[var(--fs-label)] text-foreground/52">{styleProfile.source === "user" ? "来自我的风格" : styleProfile.source === "external" ? "外部导入" : "系统预设"}</span> : <span className="text-[var(--fs-label)] text-foreground/40">未设置</span>}</div>
                <div className="flex flex-col gap-3 rounded-lg bg-surface-active p-3 lg:flex-row lg:items-center">
                    {selectedStyle ? <img src={selectedStyle.imageUrl} width="160" height="90" alt={`${selectedStyle.title}画风示意`} className="aspect-video w-40 shrink-0 rounded-md object-cover" /> : <span className="grid aspect-video w-40 shrink-0 place-items-center rounded-md bg-foreground/5 text-foreground/35"><Palette className="size-5" /></span>}
                    <div className="min-w-0 flex-1">
                        <div className="text-sm font-semibold">{styleProfile?.title || selectedStyle?.title || "尚未设置项目画风"}</div>
                        <p className="mt-1 line-clamp-2 text-xs leading-5 text-foreground/48">{styleProfile?.description || selectedStyle?.description || "从系统风格开始，或创建可自由编辑的项目视觉规范。"}</p>
                        {styleProfile ? <div className="mt-2 flex flex-wrap gap-1">{styleProfile.tags.map((tag) => <span key={tag} className="rounded bg-foreground/10 px-1.5 py-0.5 text-[var(--fs-tiny)] text-foreground/55">{tag}</span>)}</div> : null}
                    </div>
                    <div className="flex shrink-0 flex-wrap gap-2"><Button icon={<Eye className="size-3.5" />} disabled={!selectedStyle} onClick={() => setStyleDetail(selectedStyle || null)}>查看规范</Button><Button icon={<Pencil className="size-3.5" />} disabled={!styleProfile} onClick={() => { setStyleEditorRequested(true); setStylePickerOpen(true); }}>编辑画风</Button><Button icon={<Palette className="size-3.5" />} onClick={() => { setStyleEditorRequested(false); setStylePickerOpen(true); }}>{selectedStyle ? "更换画风" : "选择画风"}</Button></div>
                </div>
                {stylePresetId !== (project.stylePresetId || "") || styleProfileJson !== (project.styleProfileJson || "") ? <div className="mt-2 flex flex-wrap items-center justify-between gap-2 rounded-md bg-amber-500/5 px-3 py-2 text-[var(--fs-label)] leading-5 text-amber-600 dark:text-amber-400"><span>画风已选好，保存设置后才对分镜和生成生效。</span><Button size="small" type="primary" icon={<Save className="size-3.5" />} disabled={!name.trim()} loading={saveMutation.isPending} onClick={() => saveMutation.mutate()}>保存设置</Button></div> : null}
                {styleProfile ? <div className="mt-2 grid grid-cols-2 gap-2 text-xs sm:grid-cols-5"><StyleMetric label="执行策略" value={styleProfile.executionPolicy === "strict-assets" ? "严格校验" : "兼容降级"} /><StyleMetric label="绑定资产" value={`${styleProfile.assets.length} 个`} /><StyleMetric label="已启用" value={`${enabledStyleAssets.length} 个`} /><StyleMetric label="图片执行" value={styleExecutionStatusLabel(styleExecutionPlans?.image.status)} /><StyleMetric label="视频执行" value={styleExecutionStatusLabel(styleExecutionPlans?.video.status)} /></div> : null}
                {styleExecutionPlans && (styleExecutionPlans.image.warnings.length || styleExecutionPlans.video.warnings.length) ? <div className="mt-2 grid gap-1 rounded-md bg-amber-500/5 px-3 py-2 text-[var(--fs-label)] leading-5 text-amber-600 dark:text-amber-400">{styleExecutionPlans.image.warnings.length ? <p>图片：{styleExecutionPlans.image.warnings.join("；")}</p> : null}{styleExecutionPlans.video.warnings.length ? <p>视频：{styleExecutionPlans.video.warnings.join("；")}</p> : null}</div> : null}
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
            <CanvasStylePickerModal open={stylePickerOpen} value={stylePresetId} currentProfile={styleProfile} startInEditor={styleEditorRequested} onClose={() => { setStylePickerOpen(false); setStyleEditorRequested(false); }} onSelect={(preset) => { applyStyle(preset); setStylePickerOpen(false); setStyleEditorRequested(false); }} />
            <CanvasStyleDetailModal open={Boolean(styleDetail)} preset={styleDetail} selected={styleDetail?.id === stylePresetId} onClose={() => setStyleDetail(null)} onSelect={(preset) => { applyStyle(preset); setStyleDetail(null); }} />
        </div>
    );

    function applyStyle(preset: CanvasStylePreset) {
        setStylePresetId(preset.id);
        setStyleProfileJson(serializeStyleProfile(preset.profile || createStyleProfileSnapshot(preset)));
    }
}

function StyleMetric({ label, value }: { label: string; value: string }) {
    return <div className="min-w-0 rounded-md bg-surface-active px-3 py-2"><span className="block text-[var(--fs-tiny)] text-foreground/40">{label}</span><span className="mt-0.5 block truncate font-medium text-foreground/65">{value}</span></div>;
}

function styleExecutionStatusLabel(status?: "ready" | "degraded" | "blocked") {
    return status === "blocked" ? "不可执行" : status === "degraded" ? "降级执行" : "完整执行";
}

function Field({ label, className = "", children }: { label: string; className?: string; children: ReactNode }) {
    return <label className={`grid gap-1.5 text-xs ${className}`}><span className="font-medium text-foreground/62">{label}</span>{children}</label>;
}
