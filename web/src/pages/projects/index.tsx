import { CollectionToolbar } from "@/components/layout/collection-toolbar";
import { DeleteButton } from "@/components/ui/base/buttons/delete-button";
import { CachedResourceImage } from "@/components/cached-resource-image";
import { MediaPlaceholder } from "@/components/ui/product/media-placeholder";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Form, Input, Modal } from "antd";
import { ArrowRight, BookOpenText, FileText, FolderKanban, Images, LayoutGrid, Palette, Plus, ScrollText, Search, Sparkles } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router";

import { CollectionGrid, PageHeader, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceLoadingState, WorkspaceState } from "@/components/layout/workspace-state";
import { CanvasStylePickerModal, resolveCanvasStylePreset, resolveProjectCanvasStyle, type CanvasStylePreset } from "@/components/canvas/canvas-style-picker-modal";
import { resourceFileUrl } from "@/services/api/resources";
import { ModelPicker } from "@/components/model-picker";
import { createStyleProfileSnapshot, parseStyleProfile, serializeStyleProfile } from "@/lib/canvas/style-profile";
import { projectSummaryCompletion, projectSummaryStage } from "@/lib/project-workbench";
import { shouldRetryProjectNameConflict } from "@/lib/project-name-conflict";
import { settingsPath } from "@/lib/settings-navigation";
import { PromptTemplateOperation, parseGeneratedStory, promptTemplateTaskPlaceholder, shortDramaOutlineVariables } from "@/lib/prompts";
import { runBackendGenerationTask } from "@/services/api/generation-task";
import { createProject, deleteProject, importProjectUnits, listProjects, type ProjectSummary } from "@/services/api/projects";
import { modelDisplayName, useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { sourceTypeLabel } from "./detail/shared";
import { Select } from "@/components/ui/base/select";
import type { DramaAuthMode } from "@/services/api/drama";
import { DEFAULT_DRAMA_MODEL, DRAMA_AUTH_OPTIONS, SEGMENT_SECONDS_OPTIONS, dramaModelOptions, findDramaModel, preferredDramaModel, useDramaChannelFamilies, useDramaFamilyRatios, useDramaModels } from "./drama-model";
import { PROJECT_CONTENT_TYPES, canvasStyleMedium, styleMatchesContentType, type ProjectContentType } from "@/lib/canvas/project-content-type";
import { lockDramaStyle, type DramaStyleLibraryItem } from "@/services/api/drama-style-library";
import { CHAPTER_IMPORT_QUERY } from "./detail/chapter-import-query";
import { followStyleLockAfterCreate } from "./detail/chapter-style-lock";
import { startStyleLock } from "./detail/project-chapter-ai";
import { STYLE_LOCK_TEXT_CHARS, StyleLibraryPicker, StyleReferenceImagesPicker, uploadStyleReferenceImages } from "./project-style-inputs";

type ProjectForm = { contentType: ProjectContentType; name: string; aspectRatio: string; sourceType: string; segmentSeconds: number; generationModel: string; authMode: DramaAuthMode };

/** 建项目时的「项目风格」：风格库选现成的，或写风格描述 / 传参考图（两者可以同时给）去分析。 */
type ProjectStyleMode = "library" | "text" | "images";

const PROJECT_STYLE_MODES: Array<{ value: ProjectStyleMode; label: string }> = [
    { value: "library", label: "风格库" },
    { value: "text", label: "写风格描述" },
    { value: "images", label: "传参考图" },
];

const PROJECT_ASPECT_RATIOS = [
    { label: "9:16 竖屏", value: "9:16" },
    { label: "16:9 横屏", value: "16:9" },
    { label: "1:1 方形", value: "1:1" },
    { label: "4:3 标准横屏", value: "4:3" },
];

export default function ProjectsPage() {
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { message, modal } = App.useApp();
    const effectiveConfig = useEffectiveConfig();
    const [createForm] = Form.useForm<ProjectForm>();
    const [searchParams, setSearchParams] = useSearchParams();
    const [keyword, setKeyword] = useState("");
    const [status, setStatus] = useState<"all" | "active" | "archived">("all");
    const [sort, setSort] = useState<"updated" | "progress" | "name">("updated");
    const [storyDraft, setStoryDraft] = useState("");
    const [createSource, setCreateSource] = useState<"blank" | "novel" | "script" | "text">("blank");
    const [selectedStyle, setSelectedStyle] = useState<CanvasStylePreset | null>(null);
    const [stylePickerOpen, setStylePickerOpen] = useState(false);
    const [generateModel, setGenerateModel] = useState("");
    const [generateChapterCount, setGenerateChapterCount] = useState("5");
    const [generateStructure, setGenerateStructure] = useState("单线推进");
    const [generateChapterLength, setGenerateChapterLength] = useState("中");
    const [generateWordCount, setGenerateWordCount] = useState("800");
    const [generatePerspective, setGeneratePerspective] = useState("第三人称");
    const [generateTone, setGenerateTone] = useState("平稳叙事");
    const [generateCharacterScale, setGenerateCharacterScale] = useState("3-4 个");
    const dramaModels = useDramaModels();
    const [launcherModel, setLauncherModel] = useState("");
    const [launcherAuth, setLauncherAuth] = useState<DramaAuthMode>("step");
    const dramaFamilies = useDramaChannelFamilies(dramaModels.data);
    const defaultDramaModel = preferredDramaModel(dramaModels.data, dramaFamilies);
    const dramaRatios = useDramaFamilyRatios(dramaModels.data);
    const createModelId = Form.useWatch("generationModel", createForm);
    const createModel = findDramaModel(dramaModels.data, createModelId);
    const createAuth = Form.useWatch("authMode", createForm);
    const createContentType = Form.useWatch("contentType", createForm);
    const createAspectRatio = Form.useWatch("aspectRatio", createForm);
    const createSegmentSeconds = Form.useWatch("segmentSeconds", createForm);
    const createModelOptions = useMemo(() => dramaModelOptions(dramaModels.data, dramaFamilies, { minSeconds: createSegmentSeconds, aspectRatio: createAspectRatio, ratios: dramaRatios }), [createAspectRatio, createSegmentSeconds, dramaFamilies, dramaModels.data, dramaRatios]);
    // 风格档案不再让用户选：后端按画风对照表推出。项目风格（风格锚 + 主基调色卡）在这里定：风格库选现成的，或写描述 / 传参考图建完项目去分析。
    const [styleMode, setStyleMode] = useState<ProjectStyleMode>("library");
    const [styleLibraryItem, setStyleLibraryItem] = useState<DramaStyleLibraryItem>();
    const [styleText, setStyleText] = useState("");
    const [styleImages, setStyleImages] = useState<File[]>([]);
    const [styleTextModel, setStyleTextModel] = useState("");
    const [generating, setGenerating] = useState(false);
    const [generationStatus, setGenerationStatus] = useState("");
    const [generationPreview, setGenerationPreview] = useState("");
    const createOpen = searchParams.get("create") === "1";
    const setCreateOpen = (open: boolean) => {
        const next = new URLSearchParams(searchParams);
        if (open) next.set("create", "1");
        else next.delete("create");
        setSearchParams(next, { replace: true });
    };
    const openCreate = (source: "blank" | "novel" | "script" | "text") => {
        setCreateSource(source);
        setCreateOpen(true);
    };
    // 每次打开只初始化一次：对话框里切换内容来源不能把已选的画幅、时长、模型重置掉。
    const createInitializedRef = useRef(false);
    useEffect(() => {
        if (!createOpen) {
            createInitializedRef.current = false;
            return;
        }
        if (createInitializedRef.current) return;
        createInitializedRef.current = true;
        createForm.setFieldsValue({
            name: storyDraft.trim().slice(0, 24) || "",
            sourceType: createSource,
            aspectRatio: "9:16",
            segmentSeconds: SEGMENT_SECONDS_OPTIONS[0],
            generationModel: launcherModel || defaultDramaModel,
            authMode: launcherAuth,
        });
    }, [createForm, createOpen, createSource, storyDraft, launcherModel, launcherAuth, defaultDramaModel]);
    // 模型目录晚于对话框读到时，没动过的生成模型跟着默认值走。
    useEffect(() => {
        if (createOpen && !createForm.isFieldTouched("generationModel")) createForm.setFieldValue("generationModel", launcherModel || defaultDramaModel);
    }, [createForm, createOpen, launcherModel, defaultDramaModel]);
    // 在外面先选了画风再打开对话框：按画风的媒介把类型带上。
    useEffect(() => {
        if (!createOpen || createForm.getFieldValue("contentType") || !selectedStyle) return;
        const medium = canvasStyleMedium(selectedStyle);
        const matched = PROJECT_CONTENT_TYPES.find((item) => (item.media as readonly string[]).includes(medium));
        if (matched) createForm.setFieldValue("contentType", matched.value);
    }, [createForm, createOpen, selectedStyle]);
    useEffect(() => {
        if (createOpen) return;
        setStyleMode("library");
        setStyleLibraryItem(undefined);
        setStyleText("");
        setStyleImages([]);
    }, [createOpen]);
    // 单段时长或画幅变了、当前模型不在可选范围里：换成第一个可选的（优先有渠道的）。
    useEffect(() => {
        if (!createOpen || !dramaModels.data) return;
        if (createModelOptions.some((option) => option.value === createModelId && !option.disabled)) return;
        const enabled = createModelOptions.filter((option) => !option.disabled);
        const next = enabled.find((option) => dramaFamilies?.has(option.value)) || enabled[0];
        if (next && next.value !== createModelId) createForm.setFieldValue("generationModel", next.value);
    }, [createForm, createModelId, createModelOptions, createOpen, dramaFamilies, dramaModels.data]);
    // 换了类型：已选画风不属于新类型就清空；风格库按类型归档，已选的条目也清空。
    useEffect(() => {
        if (createOpen && createContentType && selectedStyle && !styleMatchesContentType(selectedStyle, createContentType)) setSelectedStyle(null);
    }, [createContentType, createOpen, selectedStyle]);
    useEffect(() => {
        if (styleLibraryItem && styleLibraryItem.projectType !== createContentType) setStyleLibraryItem(undefined);
    }, [createContentType, styleLibraryItem]);

    const generateStory = async () => {
        const story = storyDraft.trim();
        if (!story || generating) return;
        const textModel = generateModel || effectiveConfig.textModel;
        if (!textModel || !effectiveConfig.textModels.includes(textModel)) {
            if (!textModel) {
                modal.warning({
                    title: "需要先选择文本模型",
                    content: "请在上方“AI 模型”中选择一个已配置的文本模型，或先到设置中完成模型渠道配置。",
                    okText: "去设置",
                    cancelText: "取消",
                    onOk: () => navigate(settingsPath("models")),
                });
            } else {
                message.error(`模型 ${textModel} 未在文本模型列表中，请重新选择`);
            }
            return;
        }
        setGenerating(true);
        setGenerationStatus("正在创建项目…");
        setGenerationPreview("");
        try {
            const project = await createUniqueProjectName(story, selectedStyle, { generationModel: launcherModel || defaultDramaModel, authMode: launcherAuth });
            setGenerationStatus("AI 正在生成故事大纲与章节…");
            const result = await runBackendGenerationTask({
                projectId: project.project.id,
                mode: "text",
                prompt: promptTemplateTaskPlaceholder("短剧大纲"),
                config: { ...effectiveConfig, model: textModel, imageModel: textModel, videoModel: textModel, textModel },
                metadata: {
                    source: "project-story-generator",
                    projectId: project.project.id,
                    promptTemplateOperation: PromptTemplateOperation.ShortDramaOutline,
                    promptTemplateVariables: shortDramaOutlineVariables({
                        story,
                        chapterCount: generateChapterCount,
                        structure: generateStructure,
                        wordCount: generateWordCount,
                        perspective: generatePerspective,
                        tone: generateTone,
                        characterScale: generateCharacterScale,
                        chapterLength: generateChapterLength,
                    }),
                },
                onTextDelta: setGenerationPreview,
            });
            const answer = result.text || "";
            const parsed = parseGeneratedStory(answer);
            if (!parsed.chapters.length) throw new Error("AI 没有返回有效的章节内容，请重试");
            setGenerationStatus(`正在导入 ${parsed.chapters.length} 个章节…`);
            await importProjectUnits(project.project.id, parsed.chapters.map((chapter: { title: string; content: string }) => ({ kind: "chapter", title: chapter.title, sourceText: chapter.content })));
            await queryClient.invalidateQueries({ queryKey: ["projects"] });
            navigate(`/projects/${project.project.id}/overview`);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "AI 生成失败，请重试");
        } finally {
            setGenerating(false);
            setGenerationStatus("");
            setGenerationPreview("");
        }
    };
    const loadMoreRef = useRef<HTMLDivElement>(null);
    const query = useInfiniteQuery({
        // 分页查询和画布页的全量项目查询不能共用缓存形状，否则两个页面会互相覆盖缓存数据。
        queryKey: ["projects", "paged"],
        queryFn: ({ pageParam }) => listProjects({ page: pageParam, pageSize: 50 }),
        initialPageParam: 1,
        getNextPageParam: (lastPage) => (lastPage.hasMore ? lastPage.page + 1 : undefined),
    });
    // 建项目之后定风格：选了风格库直接锁定（不跑分析）；写了描述或传了参考图就起风格分析（图先上传成资源）；
    // 两样都没有但选了画风，按画风当场生成主色卡并直接锁定（不回写风格库）。项目已经建好，这一步失败只提示，不回滚项目。
    const lockCreatedProjectStyle = async (projectId: string, contentType: ProjectContentType) => {
        if (styleMode === "library" && styleLibraryItem) {
            await lockDramaStyle(projectId, { libraryId: styleLibraryItem.id });
            return { locked: true };
        }
        const text = styleText.trim();
        const images = styleMode === "library" ? [] : styleImages;
        const analyze = styleMode !== "library" && (text || images.length);
        if (!analyze && !selectedStyle) return {};
        const textModel = styleTextModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!textModel || !useConfigStore.getState().isAiConfigReady(config, textModel)) throw new Error("还没有可用的文本模型，没有分析项目风格；配好模型后在章节页点「分析项目风格」");
        const referenceImages = analyze ? await uploadStyleReferenceImages(images) : [];
        const { task } = await startStyleLock({ projectId, config, projectType: contentType, styleText: analyze ? text : "", referenceImages, lockOnFinish: !analyze });
        followStyleLockAfterCreate(projectId, task.id);
        return { taskId: task.id };
    };
    const mutation = useMutation({
        mutationFn: async ({ contentType, ...input }: Parameters<typeof createProject>[0] & { contentType: ProjectContentType }) => {
            const created = await createProject(input);
            let style: { locked?: boolean; taskId?: string; error?: string } = {};
            try {
                style = await lockCreatedProjectStyle(created.project.id, contentType);
            } catch (error) {
                style = { error: error instanceof Error ? error.message : "项目风格没定上" };
            }
            return { ...created, style };
        },
        onSuccess: ({ project, style }) => {
            setCreateOpen(false);
            void queryClient.invalidateQueries({ queryKey: ["projects"] });
            if (style.error) message.warning(`项目已建好，风格没定上：${style.error}`);
            else if (style.locked) message.success("项目风格已锁定：风格锚和主基调色卡已带上");
            const imported = project.sourceType === "novel" || project.sourceType === "script";
            // 选了「导入小说」「导入剧本」：建完直接到章节页并打开导入对话框（导入剧本时导入的是剧集）；起了风格分析也去章节页，侧栏接上它的流。
            if (imported) navigate(`/projects/${project.id}/chapters?${CHAPTER_IMPORT_QUERY}=1`);
            else navigate(style.taskId ? `/projects/${project.id}/chapters` : `/projects/${project.id}/overview`);
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "项目创建失败"),
    });
    const deleteMutation = useMutation({
        mutationFn: deleteProject,
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ["projects"] });
            message.success("项目已删除");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "项目删除失败"),
    });
    const allProjects = useMemo(() => query.data?.pages.flatMap((page) => page.projects) || [], [query.data]);
    const rows = useMemo(() => {
        const normalizedKeyword = keyword.trim().toLowerCase();
        return [...allProjects]
            .filter(({ project }) => status === "all" || project.status === status)
            .filter(({ project }) => !normalizedKeyword || `${project.name} ${project.description} ${project.stylePresetId} ${parseStyleProfile(project.styleProfileJson)?.title || resolveCanvasStylePreset(project.stylePresetId)?.title || ""}`.toLowerCase().includes(normalizedKeyword))
            .sort((left, right) => {
                if (sort === "name") return left.project.name.localeCompare(right.project.name, "zh-CN");
                if (sort === "progress") return projectSummaryCompletion(right) - projectSummaryCompletion(left);
                return right.project.updatedAt.localeCompare(left.project.updatedAt);
            });
    }, [allProjects, keyword, sort, status]);
    const totalProjectCount = query.data?.pages[0]?.total ?? allProjects.length;
    useEffect(() => {
        const node = loadMoreRef.current;
        if (!node || !query.hasNextPage || query.isError) return;
        const observer = new IntersectionObserver(
            ([entry]) => {
                if (entry?.isIntersecting && !query.isFetchingNextPage) void query.fetchNextPage();
            },
            { rootMargin: "600px" },
        );
        observer.observe(node);
        return () => observer.disconnect();
    }, [query.fetchNextPage, query.hasNextPage, query.isError, query.isFetchingNextPage]);
    const hasInitialError = query.isError && !query.data;
    return (
        <WorkspacePage className="library-page project-library-page" grid>
            <details className="story-launcher-panel" aria-label="开始一部新短剧">
                <summary className="story-launcher-head">
                    <div className="story-launcher-title">
                        <span className="story-launcher-mark"><Sparkles className="size-4" /></span>
                        <div>
                            <h2>开始一部新短剧</h2>
                            <p>一句话生成章节，也可以导入小说或从空白开始</p>
                        </div>
                    </div>
                    <span className="story-launcher-expand"><Plus className="size-4" /><span>展开创作</span></span>
                </summary>
                <div className="story-launcher-main">
                    <Input.TextArea
                        className="story-launcher-input"
                        value={storyDraft}
                        onChange={(event) => setStoryDraft(event.target.value)}
                        placeholder="例如：一个失忆的快递员，每天收到十年前寄出的信件……"
                        autoSize={{ minRows: 2, maxRows: 5 }}
                        aria-label="一句话故事"
                    />
                    {selectedStyle ? <button type="button" className="story-launcher-style-chip" onClick={() => setStylePickerOpen(true)} title={selectedStyle.title}>
                        <img src={selectedStyle.imageUrl} alt="" />
                        <span>{selectedStyle.title}</span>
                    </button> : null}
                </div>
                    <div className="story-launcher-actions">
                        <Button icon={<FolderKanban />} onClick={() => openCreate("blank")}>空白项目</Button>
                        <Button icon={<FileText />} onClick={() => openCreate("novel")}>导入小说</Button>
                        <Button icon={<Palette />} onClick={() => setStylePickerOpen(true)}>{selectedStyle ? "更换画风" : "选画风"}</Button>
                        <ModelPicker
                            config={effectiveConfig}
                            value={generateModel || effectiveConfig.textModel}
                            onChange={setGenerateModel}
                            capability="text"
                            variant="creation"
                            placeholder="选择文本模型"
                            showSelectedPrice={false}
                            showOptionPrices
                            popoverClassName="agent-model-picker-popover"
                        />
                        <Button type="default" icon={<Sparkles className="size-3.5" />} disabled={!storyDraft.trim() || generating} loading={generating} onClick={() => void generateStory()}>AI 生成章节</Button>
                        <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openCreate(createSource)}>开始创作</Button>
                    </div>
                <details className="story-launcher-options">
                    <summary>故事设置 · {generateChapterCount} 章 · {generatePerspective} · {generateTone}</summary>
                <div className="story-launcher-controls">
                    <label><span>生成模型</span><Select size="small" className="min-w-52" value={launcherModel || defaultDramaModel} onChange={setLauncherModel} options={dramaModelOptions(dramaModels.data, dramaFamilies)} /></label>
                    <label><span>授权档位</span><Select size="small" className="min-w-28" value={launcherAuth} onChange={setLauncherAuth} options={DRAMA_AUTH_OPTIONS.map(({ label, value }) => ({ label, value }))} /></label>
                    <label><span>章节数量</span><Select size="small" className="min-w-28" value={generateChapterCount} onChange={setGenerateChapterCount} options={[{ label: "3 章", value: "3" }, { label: "5 章", value: "5" }, { label: "8 章", value: "8" }, { label: "10 章", value: "10" }]} /></label>
                    <label><span>叙事结构</span><Select size="small" className="min-w-32" value={generateStructure} onChange={setGenerateStructure} options={[{ label: "单线推进", value: "单线推进" }, { label: "双线并行", value: "双线并行" }, { label: "群像多线", value: "群像多线" }, { label: "反转嵌套", value: "反转嵌套" }]} /></label>
                    <label><span>章节篇幅</span><Select size="small" className="min-w-28" value={generateChapterLength} onChange={setGenerateChapterLength} options={[{ label: "精炼", value: "短" }, { label: "均衡", value: "中" }, { label: "丰满", value: "长" }]} /></label>
                    <label><span>单章字数</span><Select size="small" className="min-w-28" value={generateWordCount} onChange={setGenerateWordCount} options={[{ label: "500 字", value: "500" }, { label: "800 字", value: "800" }, { label: "1200 字", value: "1200" }, { label: "2000 字", value: "2000" }]} /></label>
                    <label><span>叙述视角</span><Select size="small" className="min-w-28" value={generatePerspective} onChange={setGeneratePerspective} options={[{ label: "第三人称", value: "第三人称" }, { label: "第一人称", value: "第一人称" }, { label: "多视角", value: "多视角" }]} /></label>
                    <label><span>故事基调</span><Select size="small" className="min-w-32" value={generateTone} onChange={setGenerateTone} options={[{ label: "平稳叙事", value: "平稳叙事" }, { label: "轻松喜剧", value: "轻松喜剧" }, { label: "紧张悬疑", value: "紧张悬疑" }, { label: "热血成长", value: "热血成长" }, { label: "甜宠治愈", value: "甜宠治愈" }]} /></label>
                    <label><span>角色规模</span><Select size="small" className="min-w-28" value={generateCharacterScale} onChange={setGenerateCharacterScale} options={[{ label: "2 个", value: "2 个" }, { label: "3-4 个", value: "3-4 个" }, { label: "5-6 个", value: "5-6 个" }]} /></label>
                </div>
                </details>
            </details>
            <CollectionToolbar active={Boolean(keyword || status !== "all" || sort !== "updated")} onReset={() => { setKeyword(""); setStatus("all"); setSort("updated"); }}>
                <Input allowClear className="app-list-search" prefix={<Search className="size-4 text-foreground/40" />} value={keyword} placeholder="搜索项目、简介或画风" onChange={(event) => setKeyword(event.target.value)} />
                <Select className="w-32" value={status} onChange={setStatus} options={[{ label: "全部状态", value: "all" }, { label: "进行中", value: "active" }, { label: "已归档", value: "archived" }]} />
                <Select className="w-32" value={sort} onChange={setSort} options={[{ label: "最近更新", value: "updated" }, { label: "章节进度", value: "progress" }, { label: "项目名称", value: "name" }]} />
            </CollectionToolbar>

            {hasInitialError ? <WorkspaceErrorState description={query.error instanceof Error ? query.error.message : "项目列表加载失败"} onRetry={() => void query.refetch()} /> : null}
            {query.isLoading ? <WorkspaceLoadingState label="正在整理项目" detail="读取章节、画布与资产进度" /> : null}
            {!query.isLoading && !hasInitialError && rows.length ? (
                <CollectionGrid className="library-grid project-library-grid">
                    {rows.map((row) => <ProjectRow key={row.project.id} row={row} onDelete={() => deleteMutation.mutateAsync(row.project.id)} />)}
                </CollectionGrid>
            ) : null}
            {!query.isLoading && !hasInitialError ? <div ref={loadMoreRef} className="library-load-more" aria-live="polite">
                {query.isFetchingNextPage ? "正在加载更多项目…" : query.isError ? <button type="button" onClick={() => void query.fetchNextPage()}>加载更多失败，点击重试</button> : query.hasNextPage ? "继续下滑加载更多（每页 50 条）" : allProjects.length ? `已加载全部 ${totalProjectCount} 个项目` : null}
            </div> : null}
            {!query.isLoading && !rows.length && !hasInitialError && (keyword || status !== "all") ? (
                <WorkspaceState
                    icon="projects"
                    title={keyword || status !== "all" ? "没有匹配的项目" : "创建第一个故事项目"}
                    description={keyword || status !== "all" ? "调整搜索词或状态筛选后再试。" : "项目会集中保存章节、项目画布、角色场景和制作进度。自由试图可从画布开始。"}
                    action={!keyword && status === "all" ? <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => setCreateOpen(true)}>创建项目</Button> : undefined}
                />
            ) : null}

            <Modal className="library-modal" title="创建短剧项目" open={createOpen} footer={null} destroyOnHidden onCancel={() => setCreateOpen(false)} width={600} styles={{ body: { paddingTop: 12 } }}>
                <Form<ProjectForm>
                    form={createForm}
                    layout="vertical"
                    initialValues={{ aspectRatio: "9:16", sourceType: "blank", segmentSeconds: SEGMENT_SECONDS_OPTIONS[0], generationModel: DEFAULT_DRAMA_MODEL, authMode: "step" }}
                    onFinish={({ contentType, ...values }) => mutation.mutate({ ...values, contentType, type: "short-drama", ...(selectedStyle ? { stylePresetId: selectedStyle.id, styleProfileJson: serializeStyleProfile(selectedStyle.profile || createStyleProfileSnapshot(selectedStyle)) } : {}) })}
                >
                    <Form.Item name="contentType" label="类型" rules={[{ required: true, message: "请先选类型" }]} extra="类型用来筛选画风和风格库；风格档案按类型和画风自动推出。">
                        <ContentTypePicker />
                    </Form.Item>
                    <Form.Item label="项目风格" extra={styleMode === "library" ? "选中即锁定：风格锚和主基调色卡直接带进项目，不再分析。" : "建完项目在章节页侧栏分析，出风格锚和主基调色卡，满意再锁定；描述和参考图可以同时给。"}>
                        <div className="mb-2 grid grid-cols-3 gap-2" role="radiogroup" aria-label="项目风格">
                            {PROJECT_STYLE_MODES.map((item) => (
                                <button key={item.value} type="button" role="radio" aria-checked={styleMode === item.value} className={styleMode === item.value ? "app-story-source is-active" : "app-story-source"} onClick={() => setStyleMode(item.value)}>
                                    <span>{item.label}</span>
                                    {item.value === "text" && styleText.trim() ? <span className="text-[var(--fs-micro)] text-foreground/45">已写</span> : null}
                                    {item.value === "images" && styleImages.length ? <span className="text-[var(--fs-micro)] text-foreground/45">{styleImages.length} 张</span> : null}
                                </button>
                            ))}
                        </div>
                        {styleMode === "library" ? <StyleLibraryPicker projectType={createContentType} value={styleLibraryItem?.id} onChange={setStyleLibraryItem} /> : null}
                        {styleMode === "text" ? <Input.TextArea value={styleText} onChange={(event) => setStyleText(event.target.value)} autoSize={{ minRows: 3, maxRows: 6 }} maxLength={STYLE_LOCK_TEXT_CHARS} showCount className="!mb-5" placeholder="例如：都市冷调悬疑，低饱和、实景光源，少用霓虹；人物写实不精修" /> : null}
                        {styleMode === "images" ? <StyleReferenceImagesPicker value={styleImages} onChange={setStyleImages} /> : null}
                        {styleMode !== "library" ? (
                            <div className="mt-2 grid gap-1">
                                <ModelPicker config={effectiveConfig} value={styleTextModel || effectiveConfig.textModel} onChange={setStyleTextModel} capability="text" variant="creation" fullWidth placeholder="选择分析风格用的文本模型" showSelectedPrice={false} />
                                <span className="text-[var(--fs-tiny)] leading-[18px] text-foreground/45">传了参考图要选能读图的文本模型。都不填但选了画风时，按画风当场生成主色卡存在项目上。</span>
                            </div>
                        ) : null}
                    </Form.Item>
                    <div className="mb-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
                        <button type="button" className={createSource === "blank" ? "app-story-source is-active" : "app-story-source"} onClick={() => { setCreateSource("blank"); createForm.setFieldValue("sourceType", "blank"); }}><FolderKanban className="size-4" /><span>空白开始</span></button>
                        <button type="button" className={createSource === "novel" ? "app-story-source is-active" : "app-story-source"} onClick={() => { setCreateSource("novel"); createForm.setFieldValue("sourceType", "novel"); }}><FileText className="size-4" /><span>导入小说</span></button>
                        <button type="button" className={createSource === "script" ? "app-story-source is-active" : "app-story-source"} onClick={() => { setCreateSource("script"); createForm.setFieldValue("sourceType", "script"); }}><ScrollText className="size-4" /><span>导入剧本</span></button>
                        <button type="button" className={createSource === "text" ? "app-story-source is-active" : "app-story-source"} onClick={() => { setCreateSource("text"); createForm.setFieldValue("sourceType", "text"); }}><BookOpenText className="size-4" /><span>粘贴文本</span></button>
                    </div>
                    <Form.Item name="sourceType" hidden><Input /></Form.Item>
                    <Form.Item name="name" label="项目名称" rules={[{ required: true, whitespace: true, message: "请输入项目名称" }]}><Input autoFocus placeholder="例如：长安夜行" /></Form.Item>
                    <Form.Item label="项目画风"><button type="button" className="app-story-modal-style" disabled={!createContentType} onClick={() => setStylePickerOpen(true)}>{selectedStyle ? <><img src={selectedStyle.imageUrl} alt="" /><span>{selectedStyle.title}</span><em>更换</em></> : <><Palette className="size-4" /><span>{createContentType ? "选择项目画风（生成分镜前必选）" : "先选类型，再选画风"}</span></>}</button></Form.Item>
                    <div className="grid grid-cols-2 gap-3">
                        <Form.Item name="aspectRatio" label="默认画幅"><Select options={PROJECT_ASPECT_RATIOS} /></Form.Item>
                        <Form.Item name="segmentSeconds" label="单段最长时长" extra="生成模型只列一段能做到这么长的；建项后可在设置里改。"><Select options={SEGMENT_SECONDS_OPTIONS.map((value) => ({ label: `${value} 秒`, value }))} /></Form.Item>
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                        <Form.Item name="generationModel" label="生成模型" rules={[{ required: true, message: "请选择生成模型" }]} extra={createModel ? (dramaFamilies && !dramaFamilies.has(createModel.id) ? `当前没有 ${createModel.label} 的视频渠道，建了项目也生成不了视频；先在设置里配置渠道，或换一个模型。` : `一段 = 一次生成，最长 ${createModel.duration.max} 秒；建项后可在设置里改，但已有超长的段需要先拆分。`) : undefined}><Select loading={dramaModels.isLoading} options={createModelOptions} /></Form.Item>
                        <Form.Item name="authMode" label="授权档位" extra={DRAMA_AUTH_OPTIONS.find((item) => item.value === createAuth)?.hint}><Select options={DRAMA_AUTH_OPTIONS.map(({ label, value }) => ({ label, value }))} /></Form.Item>
                    </div>
                    <p className="-mt-1 mb-5 text-xs leading-5 text-foreground/48">{createSource === "novel" ? "创建后直接进入章节页导入小说，再用「剧本优化」改编成剧集。" : createSource === "script" ? "创建后直接进入章节页导入剧本，每集成为一个剧集，「剧本优化」只调节奏。" : "创建后先进入项目概览。"}章节和参考资产可以逐步补充；画风可以晚点选，但生成分镜前必须选好。</p>
                    <div className="flex justify-end gap-2"><Button onClick={() => setCreateOpen(false)}>取消</Button><Button type="primary" htmlType="submit" loading={mutation.isPending}>创建项目</Button></div>
                </Form>
            </Modal>
            <CanvasStylePickerModal
                open={stylePickerOpen}
                value={selectedStyle?.id}
                contentType={createOpen ? createContentType : undefined}
                onClose={() => setStylePickerOpen(false)}
                onSelect={(preset) => { setSelectedStyle(preset); setStylePickerOpen(false); }}
            />
            <Modal className="library-modal" title="AI 生成章节" open={generating} footer={null} closable={false} mask={{ closable: false }} keyboard={false} width={760}>
                <div className="app-story-generating">
                    <div className="app-story-generating-head">
                        <span className="app-story-generating-mark"><Sparkles className="size-4" /></span>
                        <div className="min-w-0">
                            <p>AI 正在创作</p>
                            <span className="block text-[var(--fs-tiny)] text-foreground/45">正在生成剧名、简介与章节</span>
                        </div>
                        {generateModel || effectiveConfig.textModel ? <span className="app-story-generating-model">{modelDisplayName(effectiveConfig, generateModel || effectiveConfig.textModel)}</span> : null}
                    </div>
                    <div className="app-story-generating-progress" aria-hidden="true" />
                    <div className="app-story-generating-grid">
                        <div className="app-story-generating-story">
                            <span className="app-story-generating-caption">故事起点</span>
                            <p>{storyDraft.trim() || "等待故事输入"}</p>
                            <span className="app-story-generating-meta">{generateChapterCount} 章 · 每章约 {generateWordCount} 字 · {generateStructure} · {generatePerspective}</span>
                            {selectedStyle ? <span className="app-story-generating-style"><img src={selectedStyle.imageUrl} alt="" /><span>{selectedStyle.title}</span></span> : null}
                        </div>
                        <ol className="app-story-generating-steps">
                            {generationSteps.map((step) => {
                                const state = generationStatus.startsWith(step.label) ? "is-active" : generationStepDone(step.label, generationStatus) ? "is-done" : "";
                                return <li key={step.label} className={state}><span className="app-story-generating-step-dot" /><span>{step.label}</span><em>{state === "is-active" ? "进行中" : state === "is-done" ? "完成" : "等待"}</em></li>;
                            })}
                        </ol>
                    </div>
                    <div className="app-story-generating-preview">
                        <div className="app-story-generating-preview-head"><span>实时草稿</span><span className="app-story-generating-live" /><em>{generationPreview ? "正在输出" : "等待模型输出"}</em></div>
                        <pre>{generationPreview}</pre>
                    </div>
                </div>
            </Modal>
        </WorkspacePage>
    );
}

async function createUniqueProjectName(story: string, selectedStyle: CanvasStylePreset | null, drama: { generationModel: string; authMode: DramaAuthMode }) {
    const base = story.trim().slice(0, 24);
    const buildInput = (name: string) => ({
        name,
        type: "short-drama" as const,
        aspectRatio: "9:16",
        sourceType: "blank",
        description: story.trim(),
        ...drama,
        ...(selectedStyle ? { stylePresetId: selectedStyle.id, styleProfileJson: serializeStyleProfile(selectedStyle.profile || createStyleProfileSnapshot(selectedStyle)) } : {}),
    });
    let attempt = 0;
    for (;;) {
        try {
            return await createProject(buildInput(attempt === 0 ? base : `${base}（${attempt + 1}）`));
        } catch (error) {
            if (!shouldRetryProjectNameConflict(error, attempt)) throw error;
            attempt += 1;
        }
    }
}

const generationSteps = [
    { label: "正在创建项目" },
    { label: "AI 正在生成故事大纲与章节" },
    { label: "正在导入章节" },
];

function generationStepDone(label: string, status: string) {
    if (label === "正在创建项目") return status.startsWith("AI 正在生成") || status.startsWith("正在导入");
    if (label === "AI 正在生成故事大纲与章节") return status.startsWith("正在导入");
    return false;
}

function ProjectRow({ row, onDelete }: { row: ProjectSummary; onDelete: () => Promise<unknown> }) {
    const completion = projectSummaryCompletion(row);
    const stage = projectSummaryStage(row);
    const projectStyle = resolveProjectCanvasStyle(row.project.stylePresetId, row.project.styleProfileJson);
    const styleTitle = projectStyle?.title || parseStyleProfile(row.project.styleProfileJson)?.title || resolveCanvasStylePreset(row.project.stylePresetId)?.title || (row.project.stylePresetId ? "自定义画风" : "未设置画风");
    const coverUrl = row.project.coverResourceId ? resourceFileUrl(row.project.coverResourceId) : projectStyle?.imageUrl;
    return (
        <Link to={`/projects/${row.project.id}/overview`} className="product-collection-card library-card project-library-card group">
            <span className="project-library-cover">
                {coverUrl ? <CachedResourceImage className="project-library-cover-art" src={coverUrl} alt="" fallback={<MediaPlaceholder failed />} /> : <MediaPlaceholder label="故事由此开始" />}
                <span className="project-library-cover-scrim" />
                <span className="project-library-cover-ratio">{row.project.aspectRatio}</span>
                <span className="project-library-cover-stage">{stage.label}</span>
                <span className="project-collection-delete"><DeleteButton label={`删除项目 ${row.project.name}`} description="项目章节、画布关联和素材归属将一并移除；独立画布与素材库原始素材会保留。此操作不可撤销。" onConfirm={onDelete} /></span>
            </span>
            <span className="project-library-body">
                <span className="project-library-heading"><strong title={row.project.name}>{row.project.name}</strong>{row.project.status === "archived" ? <em>已归档</em> : null}<ArrowRight className="project-library-arrow size-4" /></span>
                <span className="project-library-subtitle">{styleTitle} · {sourceTypeLabel(row.project.sourceType)}</span>
                <span className="project-library-progress"><span><span>{row.completedUnitCount}/{row.unitCount} 章</span><span>{completion}%</span></span><i><b style={{ width: `${completion}%` }} /></i></span>
                <span className="project-library-stats"><ProjectCount icon={<BookOpenText className="size-3.5" />} label="章节" value={row.unitCount} /><ProjectCount icon={<LayoutGrid className="size-3.5" />} label="画布" value={row.canvasCount} /><ProjectCount icon={<Images className="size-3.5" />} label="资产" value={row.assetCount} /></span>
            </span>
        </Link>
    );
}

function ContentTypePicker({ value, onChange }: { value?: ProjectContentType; onChange?: (value: ProjectContentType) => void }) {
    return (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4" role="radiogroup" aria-label="类型">
            {PROJECT_CONTENT_TYPES.map((item) => <button key={item.value} type="button" role="radio" aria-checked={value === item.value} title={item.hint} className={value === item.value ? "app-story-source is-active" : "app-story-source"} onClick={() => onChange?.(item.value)}><span>{item.label}</span></button>)}
        </div>
    );
}

function ProjectCount({ icon, label, value }: { icon: ReactNode; label: string; value: number }) {
    return <span className="inline-flex items-center gap-1.5" title={`${value} ${label}`}><span className="text-foreground/32">{icon}</span><strong className="font-medium tabular-nums text-foreground/65">{value}</strong><span>{label}</span></span>;
}
