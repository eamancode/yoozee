import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Dropdown, Input } from "antd";
import { FileText, FileUp, Pencil, Sparkles, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";

import { Select } from "@/components/ui/base/select";
import { detectProjectSource, formatSourceChars, plainTextToHtml, sourceCharCount, sourceDetectionLabel, suggestProjectName, type ProjectSourceKind } from "@/lib/project-source";
import { projectVisualTypeLabel } from "@/lib/project-visual-type";
import { listDramaStyleLibrary, dramaStyleLibraryCoverUrl, dramaStyleLibraryKey, sortDramaStyleLibrary, type DramaStyleLibraryItem } from "@/services/api/drama-style-library";
import type { CreateProjectInput, ProjectVisualType } from "@/services/api/projects";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

import { continueProjectPipeline } from "../detail/project-chapter-ai";
import { dramaRunModeLabel, findDramaModel, preferredDramaModel, useDramaChannelFamilies, useDramaModels } from "../drama-model";
import { uploadStyleReferenceImages } from "../project-style-inputs";
import { ComposerSectionShell, ProductionSection, StyleSourceSection, VisualTypeCards } from "./composer-sections";
import { composerSectionOfError, createProjectWithUniqueName, generateIdeaChapters, type ComposerSection } from "./create-project-flow";
import { clearProjectDraft, EMPTY_PROJECT_DRAFT, loadProjectDraft, projectDraftHasContent, saveProjectDraft, type ProjectCreateDraft } from "./project-draft";
import { importedUnits, SourceImportModal, type ImportedProjectSource } from "./source-import-modal";
import "../project-create.css";

const KIND_LABELS: Record<ProjectSourceKind, string> = { idea: "创意", script: "剧本", novel: "小说" };
const IDEA_EPISODES = [3, 5, 8, 10, 12, 20];
const IDEA_SECONDS = [
    { value: 30, label: "30 秒" },
    { value: 60, label: "1 分钟" },
    { value: 120, label: "2 分钟" },
    { value: 180, label: "3 分钟" },
];
/** 估段数用的语速：剧本、小说约 4 字一秒。 */
const CHARS_PER_SECOND = 4;
const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

type Notice = { text: string; undo?: () => void };

/**
 * 首页建项作曲区（一页四段）：要拍什么 → 画面类型 → 画风从哪来 → 怎么做。开始输入或拖入文件后原地展开四段，
 * 底部摘要栏一句话复述全部选择，唯一主按钮「开始制作」（Ctrl/⌘+Enter）。建项后由服务端流水线按生成模式往下做。
 */
export function ProjectComposer() {
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { message } = App.useApp();
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const userId = useUserStore((state) => state.user?.id || "");
    const [draft, setDraft] = useState<ProjectCreateDraft>(EMPTY_PROJECT_DRAFT);
    const [draftLoaded, setDraftLoaded] = useState(false);
    const [draftSaved, setDraftSaved] = useState(false);
    const [images, setImages] = useState<File[]>([]);
    const [importOpen, setImportOpen] = useState(false);
    const [importFile, setImportFile] = useState<File>();
    const [editingName, setEditingName] = useState(false);
    const [errors, setErrors] = useState<Partial<Record<ComposerSection | "summary", string>>>({});
    const [sourceNotice, setSourceNotice] = useState("");
    const [typeNotice, setTypeNotice] = useState<Notice>();
    const [status, setStatus] = useState("");
    const submitting = Boolean(status);

    // 草稿按账号 scope 存在 localforage：挂载时读回，之后每次改动存一份。
    useEffect(() => {
        let cancelled = false;
        void loadProjectDraft().then((saved) => {
            if (cancelled) return;
            setDraft(saved || EMPTY_PROJECT_DRAFT);
            setDraftLoaded(true);
        });
        return () => {
            cancelled = true;
        };
    }, [userId]);
    useEffect(() => {
        if (!draftLoaded || submitting) return;
        if (!projectDraftHasContent(draft)) {
            void clearProjectDraft();
            setDraftSaved(false);
            return;
        }
        const timer = window.setTimeout(() => void saveProjectDraft(draft).then(() => setDraftSaved(true)), 400);
        return () => window.clearTimeout(timer);
    }, [draft, draftLoaded, submitting]);

    const update = useCallback((patch: Partial<ProjectCreateDraft>) => setDraft((current) => ({ ...current, ...patch })), []);
    const clearError = (section: ComposerSection) => setErrors((current) => (current[section] || current.summary ? { ...current, [section]: undefined, summary: undefined } : current));

    const detection = useMemo(() => (draft.text.trim() ? detectProjectSource(draft.text) : undefined), [draft.text]);
    const kind: ProjectSourceKind | undefined = draft.kind || detection?.kind;
    const expanded = Boolean(draft.text.trim()) || Boolean(draft.blank);
    const sourceUnits = useMemo(() => {
        if (!draft.text.trim() || !kind || kind === "idea") return [];
        return importedUnits(draft.text, kind, draft.fileName ? draft.fileSplit !== false : true);
    }, [draft.fileName, draft.fileSplit, draft.text, kind]);
    const autoName = draft.blank && !draft.text.trim() ? "未命名项目" : suggestProjectName(draft.text, draft.fileName);
    const projectName = draft.name?.trim() || autoName;
    const label = detection && kind ? sourceDetectionLabel({ kind, chars: detection.chars, units: kind === "idea" ? 0 : sourceUnits.length }, draft.ideaEpisodes) : "";

    // 风格中心（全部类型读一次）：样张取每个类型第一张有封面的；段 3 只列当前类型，来自导演分析的和本人存的排前面。
    const libraryQuery = useQuery({ queryKey: dramaStyleLibraryKey(), queryFn: ({ signal }) => listDramaStyleLibrary(undefined, signal), enabled: expanded });
    const covers = useMemo(() => {
        const map = new Map<string, string>();
        for (const item of libraryQuery.data?.items || []) {
            const cover = dramaStyleLibraryCoverUrl(item);
            if (cover && !map.has(item.projectType)) map.set(item.projectType, cover);
        }
        return map;
    }, [libraryQuery.data]);
    const typeItems = useMemo(() => sortDramaStyleLibrary((libraryQuery.data?.items || []).filter((item) => item.projectType === draft.visualType), userId), [draft.visualType, libraryQuery.data, userId]);
    const selectedStyle = (libraryQuery.data?.items || []).find((item) => item.id === draft.styleId);

    const dramaModels = useDramaModels();
    const families = useDramaChannelFamilies(dramaModels.data);
    const recommendedModel = preferredDramaModel(dramaModels.data, families);
    const model = findDramaModel(dramaModels.data, draft.generationModel || recommendedModel);
    const totalSeconds = kind === "idea" ? draft.ideaEpisodes * draft.ideaEpisodeSeconds : detection ? sourceCharCount(draft.text) / CHARS_PER_SECOND : 0;
    const segmentEstimate = totalSeconds ? Math.max(1, Math.ceil(totalSeconds / draft.segmentSeconds)) : undefined;

    const scrollTo = (section: ComposerSection) => {
        const node = document.getElementById(`project-create-${section}`);
        node?.scrollIntoView({ behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
        node?.querySelector<HTMLElement>("[tabindex='0'], textarea, input, button")?.focus({ preventScroll: true });
    };

    // 换了画面类型：已选的风格不属于新类型就清空，原地提示并给「撤销」。
    const changeVisualType = (visualType: ProjectVisualType) => {
        clearError("visual");
        const previous = { visualType: draft.visualType, styleId: draft.styleId };
        if (draft.styleId && selectedStyle && selectedStyle.projectType !== visualType) {
            update({ visualType, styleId: undefined });
            setTypeNotice({
                text: `风格「${selectedStyle.name}」只适用于${projectVisualTypeLabel(selectedStyle.projectType)}，已清空`,
                undo: () => {
                    update(previous);
                    setTypeNotice(undefined);
                },
            });
            return;
        }
        setTypeNotice(undefined);
        update({ visualType });
    };

    const acceptImport = (source: ImportedProjectSource) => {
        if (draft.text.trim() && !draft.fileName) setSourceNotice("已替换刚才粘贴的内容");
        else setSourceNotice("");
        update({ text: source.text, fileName: source.fileName, fileSplit: source.split, kind: source.kind, blank: false, name: undefined });
        setImportOpen(false);
        setImportFile(undefined);
        clearError("source");
    };

    const removeSource = () => {
        setSourceNotice("");
        update({ text: "", fileName: undefined, fileSplit: undefined, kind: undefined, name: undefined });
    };

    // 文件拖到页面任意位置：打开导入弹窗接着解析。
    useEffect(() => {
        const hasFiles = (event: DragEvent) => Array.from(event.dataTransfer?.types || []).includes("Files");
        const over = (event: DragEvent) => {
            if (hasFiles(event)) event.preventDefault();
        };
        const drop = (event: DragEvent) => {
            if (!hasFiles(event) || importOpen) return;
            event.preventDefault();
            const file = event.dataTransfer?.files?.[0];
            if (!file) return;
            setImportFile(file);
            setImportOpen(true);
        };
        window.addEventListener("dragover", over);
        window.addEventListener("drop", drop);
        return () => {
            window.removeEventListener("dragover", over);
            window.removeEventListener("drop", drop);
        };
    }, [importOpen]);

    const submit = async () => {
        if (submitting) return;
        const next: Partial<Record<ComposerSection, string>> = {};
        if (!draft.blank && !draft.text.trim()) next.source = "粘贴剧本、小说或一句创意，或导入文件";
        if (!draft.visualType) next.visual = "先选画面类型";
        if (draft.styleMode === "library" && !draft.styleId) next.style = "从风格中心挑一个风格，或改为交给导演分析";
        if (model && model.duration.max < draft.segmentSeconds) next.production = `${model.label} 一段最长 ${model.duration.max} 秒：换模型，或改回 15 秒`;
        const textModel = effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const textReady = Boolean(textModel) && isAiConfigReady({ ...effectiveConfig, model: textModel, textModel }, textModel);
        if (kind === "idea" && !draft.blank && !textReady) next.source = "创意要先由 AI 生成章节：先在设置里配一个文本模型";
        const first = (["source", "visual", "style", "production"] as const).find((section) => next[section]);
        setErrors(next);
        if (first) {
            scrollTo(first);
            return;
        }
        const sourceAs: CreateProjectInput["sourceAs"] = !draft.text.trim() ? "blank" : kind === "idea" ? "idea" : kind === "script" ? "script" : "novel";
        try {
            let refImageIds: string[] = [];
            if (draft.styleMode === "director" && images.length) {
                setStatus("正在上传参考图…");
                try {
                    refImageIds = await uploadStyleReferenceImages(images);
                } catch (error) {
                    setErrors({ style: `参考图上传失败：${error instanceof Error ? error.message : "请重试"}` });
                    scrollTo("style");
                    return;
                }
            }
            setStatus("正在建项目…");
            const { project } = await createProjectWithUniqueName({
                name: projectName,
                sourceAs,
                source: sourceAs === "idea" ? { text: draft.text.trim() } : sourceAs === "blank" ? {} : { units: sourceUnits.map((unit) => ({ title: unit.title, sourceText: plainTextToHtml(unit.plainText) })) },
                ...(sourceAs === "idea" ? { ideaPlan: { episodes: draft.ideaEpisodes, episodeSeconds: draft.ideaEpisodeSeconds } } : {}),
                visualType: draft.visualType!,
                style: draft.styleMode === "library" ? { mode: "library", styleId: draft.styleId! } : { mode: "director", ...(draft.styleBrief.trim() ? { brief: draft.styleBrief.trim() } : {}), ...(refImageIds.length ? { refImageIds } : {}) },
                aspectRatio: draft.aspectRatio,
                segmentSeconds: draft.segmentSeconds,
                generationModel: model?.id || recommendedModel,
                runMode: draft.runMode,
            });
            // 项目已经建好：后面的生成章节、开始流水线失败只提示，进项目页由流水线写明停在哪。
            if (sourceAs === "idea") {
                setStatus("AI 正在生成章节…");
                try {
                    await generateIdeaChapters({ projectId: project.id, idea: draft.text.trim(), episodes: draft.ideaEpisodes, episodeSeconds: draft.ideaEpisodeSeconds, config: effectiveConfig, textModel });
                } catch (error) {
                    message.warning(`项目已建好，章节没生成：${error instanceof Error ? error.message : "请重试"}`);
                }
            }
            if (sourceAs !== "blank" && textReady) {
                setStatus("正在开始制作…");
                try {
                    await continueProjectPipeline(project.id, { ...effectiveConfig, model: textModel, textModel });
                } catch (error) {
                    message.warning(`项目已建好，第一步没开始：${error instanceof Error ? error.message : "到项目页点继续"}`);
                }
            }
            await clearProjectDraft();
            setDraft(EMPTY_PROJECT_DRAFT);
            setImages([]);
            void queryClient.invalidateQueries({ queryKey: ["projects"] });
            navigate(`/projects/${project.id}/overview`);
        } catch (error) {
            const section = composerSectionOfError(error);
            const text = error instanceof Error ? error.message : "项目创建失败";
            setErrors(section ? { [section]: text } : { summary: text });
            if (section) scrollTo(section);
        } finally {
            setStatus("");
        }
    };
    const submitRef = useRef(submit);
    submitRef.current = submit;

    // Ctrl/⌘+Enter 开始制作。
    useEffect(() => {
        if (!expanded) return;
        const onKey = (event: KeyboardEvent) => {
            if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !importOpen) {
                event.preventDefault();
                void submitRef.current();
            }
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [expanded, importOpen]);

    const styleSummary = draft.styleMode === "library" ? (selectedStyle ? `风格「${selectedStyle.name}」` : "从风格中心挑") : draft.styleBrief.trim() || images.length ? "交给导演分析（带参考）" : "交给导演分析";
    const sourceSummary = !draft.text.trim() ? "空项目" : kind === "idea" ? `创意 ${draft.ideaEpisodes} 集` : kind === "script" ? `剧本 ${sourceUnits.length} 集` : `小说 ${sourceUnits.length} 章`;

    return (
        <section className={`project-create${expanded ? " is-expanded" : ""}`} aria-label="开始一部新短剧">
            <div className="project-create-compose">
                <div className="project-create-compose-head">
                    <span className="project-create-mark" aria-hidden>
                        <Sparkles className="size-4" />
                    </span>
                    <div>
                        <h2>开始一部新短剧</h2>
                        <p>粘贴剧本、小说正文或一句创意，也可以把文件拖到页面任意位置</p>
                    </div>
                </div>
                {draft.fileName ? (
                    <div className="project-create-file">
                        <FileText className="size-5 shrink-0" aria-hidden />
                        <span className="min-w-0 flex-1">
                            <strong className="block truncate">{draft.fileName}</strong>
                            <span>{formatSourceChars(sourceCharCount(draft.text))}</span>
                        </span>
                        <Button size="small" onClick={() => setImportOpen(true)}>
                            替换
                        </Button>
                        <Button size="small" type="text" icon={<X className="size-3.5" />} onClick={removeSource}>
                            移除
                        </Button>
                    </div>
                ) : (
                    <Input.TextArea
                        className="project-create-input"
                        value={draft.text}
                        onChange={(event) => {
                            setSourceNotice("");
                            clearError("source");
                            update({ text: event.target.value, blank: false, ...(event.target.value.trim() ? {} : { kind: undefined }) });
                        }}
                        placeholder="例如：一个失忆的快递员，每天收到十年前寄出的信件……"
                        autoSize={{ minRows: 3, maxRows: 14 }}
                        aria-label="要拍什么"
                    />
                )}
                <div className="project-create-compose-foot">
                    {label ? (
                        <span className="project-create-detect">
                            {label}
                            <Dropdown
                                trigger={["click"]}
                                menu={{
                                    selectable: true,
                                    selectedKeys: kind ? [kind] : [],
                                    items: (Object.keys(KIND_LABELS) as ProjectSourceKind[]).map((key) => ({ key, label: `按${KIND_LABELS[key]}处理` })),
                                    onClick: ({ key }) => update({ kind: key as ProjectSourceKind }),
                                }}
                            >
                                <button type="button" className="project-create-text-button" aria-label="改识别类型">
                                    改
                                </button>
                            </Dropdown>
                        </span>
                    ) : draft.blank ? (
                        <span className="project-create-detect">空项目：建好后到章节页导入正文</span>
                    ) : null}
                    <span className="flex-1" />
                    <Button size="small" icon={<FileUp className="size-3.5" />} onClick={() => setImportOpen(true)}>
                        导入文件
                    </Button>
                    {!expanded ? (
                        <button type="button" className="project-create-text-button" onClick={() => update({ blank: true })}>
                            先建一个空项目
                        </button>
                    ) : draft.blank && !draft.text.trim() ? (
                        <button type="button" className="project-create-text-button" onClick={() => update({ blank: false })}>
                            不建空项目了
                        </button>
                    ) : null}
                </div>
                {sourceNotice ? (
                    <p className="project-create-note" role="status">
                        {sourceNotice}
                    </p>
                ) : null}
            </div>

            {expanded ? (
                <div className="project-create-sections">
                    <ComposerSectionShell id="source" index={1} title="要拍什么" required error={errors.source}>
                        <div className="project-create-name">
                            <span className="project-create-field-label">项目名</span>
                            {editingName ? (
                                <Input
                                    autoFocus
                                    size="small"
                                    className="max-w-xs"
                                    defaultValue={projectName}
                                    maxLength={60}
                                    aria-label="项目名"
                                    onBlur={(event) => {
                                        update({ name: event.target.value.trim() || undefined });
                                        setEditingName(false);
                                    }}
                                    onPressEnter={(event) => {
                                        update({ name: event.currentTarget.value.trim() || undefined });
                                        setEditingName(false);
                                    }}
                                />
                            ) : (
                                <button type="button" className="project-create-name-value" onClick={() => setEditingName(true)} aria-label={`改项目名：${projectName}`}>
                                    <strong>{projectName}</strong>
                                    <Pencil className="size-3.5" aria-hidden />
                                </button>
                            )}
                        </div>
                        {kind === "idea" && draft.text.trim() ? (
                            <div className="project-create-idea">
                                生成
                                <Select size="small" className="w-20" value={draft.ideaEpisodes} onChange={(value: number) => update({ ideaEpisodes: value })} options={IDEA_EPISODES.map((value) => ({ value, label: String(value) }))} aria-label="生成几集" />
                                集，每集约
                                <Select size="small" className="w-24" value={draft.ideaEpisodeSeconds} onChange={(value: number) => update({ ideaEpisodeSeconds: value })} options={IDEA_SECONDS} aria-label="每集时长" />
                            </div>
                        ) : null}
                    </ComposerSectionShell>

                    <ComposerSectionShell id="visual" index={2} title="画面类型" required hint="建项后不可改" error={errors.visual}>
                        <VisualTypeCards
                            value={draft.visualType}
                            covers={covers}
                            onChange={changeVisualType}
                            notice={
                                typeNotice ? (
                                    <p className="project-create-note" role="status">
                                        {typeNotice.text}
                                        {typeNotice.undo ? (
                                            <button type="button" className="project-create-text-button" onClick={typeNotice.undo}>
                                                撤销
                                            </button>
                                        ) : null}
                                    </p>
                                ) : null
                            }
                        />
                    </ComposerSectionShell>

                    <ComposerSectionShell id="style" index={3} title="画风从哪来" error={errors.style}>
                        <StyleSourceSection
                            mode={draft.styleMode}
                            visualType={draft.visualType}
                            brief={draft.styleBrief}
                            images={images}
                            styleId={draft.styleId}
                            items={typeItems}
                            loading={libraryQuery.isLoading}
                            onMode={(styleMode) => {
                                clearError("style");
                                update({ styleMode });
                            }}
                            onBrief={(styleBrief) => update({ styleBrief })}
                            onImages={setImages}
                            onStyle={(item?: DramaStyleLibraryItem) => {
                                clearError("style");
                                setTypeNotice(undefined);
                                update({ styleId: item?.id });
                            }}
                        />
                    </ComposerSectionShell>

                    <ComposerSectionShell id="production" index={4} title="怎么做" error={errors.production}>
                        <ProductionSection
                            aspectRatio={draft.aspectRatio}
                            segmentSeconds={draft.segmentSeconds}
                            segmentEstimate={segmentEstimate}
                            models={dramaModels.data?.models || []}
                            recommendedModel={recommendedModel}
                            model={model}
                            families={families}
                            runMode={draft.runMode}
                            onAspectRatio={(aspectRatio) => update({ aspectRatio })}
                            onSegmentSeconds={(segmentSeconds) => {
                                clearError("production");
                                update({ segmentSeconds });
                            }}
                            onModel={(generationModel) => {
                                clearError("production");
                                update({ generationModel });
                            }}
                            onRunMode={(runMode) => update({ runMode })}
                        />
                    </ComposerSectionShell>

                    <div className="project-create-summary" role="region" aria-label="摘要">
                        <p className="project-create-summary-text">
                            <SummaryPart onClick={() => scrollTo("source")}>「{projectName}」</SummaryPart>
                            <SummaryPart onClick={() => scrollTo("source")}>{sourceSummary}</SummaryPart>
                            <SummaryPart onClick={() => scrollTo("visual")} missing={!draft.visualType}>
                                {projectVisualTypeLabel(draft.visualType) || "还没选画面类型"}
                            </SummaryPart>
                            <SummaryPart onClick={() => scrollTo("style")}>{styleSummary}</SummaryPart>
                            <SummaryPart onClick={() => scrollTo("production")}>{draft.aspectRatio}</SummaryPart>
                            <SummaryPart onClick={() => scrollTo("production")}>每段 {draft.segmentSeconds} 秒</SummaryPart>
                            <SummaryPart onClick={() => scrollTo("production")}>{model?.label || "视频模型"}</SummaryPart>
                            <SummaryPart onClick={() => scrollTo("production")}>{dramaRunModeLabel(draft.runMode)}</SummaryPart>
                        </p>
                        <div className="project-create-summary-actions">
                            <span className="project-create-summary-status" aria-live="polite">
                                {status || errors.summary || (draftSaved ? "草稿已保存在本机" : "")}
                            </span>
                            <Button type="primary" loading={submitting} onClick={() => void submit()}>
                                开始制作
                                <kbd className="project-create-kbd">{IS_MAC ? "⌘" : "Ctrl"}+Enter</kbd>
                            </Button>
                        </div>
                    </div>
                </div>
            ) : null}

            <SourceImportModal
                open={importOpen}
                initialFile={importFile}
                onClose={() => {
                    setImportOpen(false);
                    setImportFile(undefined);
                }}
                onConfirm={acceptImport}
            />
        </section>
    );
}

function SummaryPart({ children, onClick, missing = false }: { children: ReactNode; onClick: () => void; missing?: boolean }) {
    return (
        <button type="button" className={`project-create-summary-part${missing ? " is-missing" : ""}`} onClick={onClick}>
            {children}
        </button>
    );
}
