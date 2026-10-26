import { ApiError, http } from "@/services/api/request";
import { API_ERROR_REASON } from "@/services/api/error-codes";
import type { GenerationTask } from "@/services/api/task-center";
import type { ShotArtifact } from "@/services/api/projects";

/** 可选的视频生成模型族（kb-dialects/models.json）。一段最长 duration.max 秒。 */
export type DramaModel = {
    id: string;
    label: string;
    vendor: string;
    kind: string;
    /** 渠道模型名里出现这些片段就算这个模型族。 */
    match: string[];
    duration: { min: number; max: number; step: number };
    refs: { images: number | null; videos: number | null; audios: number | null; total: number | null };
    frames_exclusive_with_refs: boolean | null;
    prompt_max_chars: number | null;
    resolutions: string[];
    calibrated: boolean;
    checked: string;
};

export type DramaRegistry = { version: string; default: string; models: DramaModel[] };

export type DramaAuthMode = "step" | "full";

/** 台词：2.1 起回指剧本 line_id，三元（pace / tone / emotion）齐全时编译成即梦四元。 */
export type DramaLine = { speaker: string; text: string; vo?: boolean; delivery?: string; line_id?: string; timbre?: string; pace?: string; tone?: string; emotion?: string; placement?: string };
export type DramaTransition = { in?: string; out?: string; cut_point?: string };
/**
 * 段内一个镜头。sound、end_state 记在镜头上：段没写 sound / rules 时后端编译从镜头取，拆段合段后跟着镜头走。
 * 2.1 由镜头表装段时另写 expression（表情链）、positions（站位）、anchors、motion、light_full（灯光七项）、transition（衔接）。
 */
export type DramaSubShot = {
    at: number;
    size?: string;
    camera?: string;
    text: string;
    lines?: DramaLine[];
    sound?: string;
    end_state?: string;
    shot_id?: string;
    risk_tags?: string[];
    start_state?: string;
    purpose?: string;
    expression?: string;
    expression_terms?: string[];
    composition?: string;
    transition?: DramaTransition;
    vo?: string;
    positions?: string;
    anchors?: string;
    motion?: string;
    light_full?: string;
};
/** 契约 segment：一段 = 一次生成，段内多镜头。 */
export type DramaSegment = {
    id: string;
    scene: string;
    title?: string;
    dur: number;
    mode: "lock_identity" | "lock_first_frame" | "text_only";
    first_frame?: string;
    last_frame?: string;
    refs?: Array<{ file: string; role: string; note?: string; state?: string }>;
    voices?: Array<{ character: string; file: string }>;
    shots: DramaSubShot[];
    cast?: Array<{ name: string; lock: string[] }>;
    screen_text?: string[];
    jimeng?: { narrative_span?: string; function?: string; strategy?: string; atmosphere?: string; light_source?: string; sound_bed?: string; group_variation?: string };
    sound?: string;
    rules?: string;
    hook?: string;
    lang?: string;
    /** 2.1：回指镜头表的镜头 id、风格档案、风险标签、同场后段的一致性桥。 */
    shot_ids?: string[];
    story_span?: string;
    profile?: string;
    reanchor?: string;
    density?: "tight" | "standard" | "dense";
    handles?: Array<{ tag: string; text: string }>;
    warnings?: string[];
    continuity_bridge?: { prev_segment: string; handoff_state: string; shared_refs: string[]; bridge_frame?: string; bridge_clip?: string; reanchor_text?: string; shared_bed?: string };
};

export type DramaIssue = { level: "error" | "warn"; message: string };
export type DramaCompileResult = {
    shotId: string;
    spec: DramaSegment;
    derived: boolean;
    result: { segment: string; model: string; duration: number; prompt: string; paste?: string; files: Array<{ file: string; tag: string; role: string }>; chars: number; warnings?: string[]; issues: DramaIssue[] };
    /** 这次编译用的编译档案（version 0 为内置默认）。 */
    compileProfile?: DramaCompileProfileRef;
};

/** 编译档案版本：装段、编译的文字（镜头画面句、台词四元、约束、⚠️ 等），0 为内置默认。 */
export type DramaCompileProfileRef = { version: number; name: string };

export type DramaSegmentProgress = {
    shotId: string;
    unitId: string;
    unitTitle?: string;
    segmentId: string;
    title: string;
    durationMs: number;
    overLimit: boolean;
    hasDialogue: boolean;
    script: boolean;
    refs: boolean;
    generated: boolean;
    reviewed: boolean;
    inCut: boolean;
    accepted: boolean;
    takes: number;
    selectedArtifactId?: string;
    missingEvidence?: string[];
};

export type DramaEndState = { key: string; label: string; done: boolean; detail: string; skill: string };

export type DramaProgress = {
    model: DramaModel;
    authMode: DramaAuthMode | "";
    segments: DramaSegmentProgress[];
    counts: { segments: number; script: number; refs: number; generated: number; takes: number; reviewed: number; inCut: number; accepted: number; overLimit: number };
    line: string;
    endStates: DramaEndState[];
    next?: DramaEndState;
};

export type DramaEvidenceKind = "transcript" | "frames" | "faces" | "levels" | "voice" | "pick" | "gate" | "review" | "delivery" | "acceptance";

export type DramaEvidence = {
    id: string;
    projectId: string;
    unitId?: string;
    shotId?: string;
    artifactId?: string;
    kind: DramaEvidenceKind;
    passed: boolean | null;
    metricsJson: string;
    resourceId?: string;
    taskId?: string;
    note: string;
    createdAt: string;
};

export type DramaGateCheck = { name: string; ok: boolean; detail: string };
export type DramaGateResult = { report: { passed: boolean; checks: DramaGateCheck[]; metrics: Record<string, unknown> }; evidence: DramaEvidence };

export function listDramaModels() {
    return http.get<{ models: DramaRegistry }>("/drama/models");
}

export function getDramaProgress(projectId: string, signal?: AbortSignal) {
    return http.get<{ progress: DramaProgress }>(`/projects/${projectId}/drama/progress`, { signal });
}

export function getDramaBrief(projectId: string) {
    return http.get<{ document: Record<string, unknown> }>(`/projects/${projectId}/drama/brief`);
}

export function putDramaBrief(projectId: string, document: Record<string, unknown>) {
    return http.put<{ document: Record<string, unknown> }>(`/projects/${projectId}/drama/brief`, { document });
}

/** 连戏表（契约 bible）：也是素材清单。每项指向文件（柚子里是资产版本 ID），checked 为 true 才算齐。 */
export type DramaBibleVariant = { label: string; age?: string; face_hair?: string; glasses?: string; marks?: string; costume?: string; lock_card?: string[]; sheet?: string; crops?: string[]; voice?: string; voice_short?: string; checked?: boolean };
export type DramaBibleCharacter = { name: string; speaks?: boolean; variants: DramaBibleVariant[] };
export type DramaBibleProp = { name: string; image?: string; states?: Array<{ when?: string; desc?: string; image?: string }>; checked?: boolean };
export type DramaBibleLocation = { name: string; plate?: string; side_a?: string; side_b?: string; fixtures?: string; light?: string; checked?: boolean };
/** 全局风格锚（导演风格 style.bible 写入 bible_json.style_anchor）。 */
export type DramaStyleAnchor = { medium?: string; art_style?: string; lighting?: string; camera?: string; grading?: string; forbidden?: string[]; era?: string; genre?: string; render_quality?: string };
/** 色彩脚本里的一个颜色。 */
export type DramaPaletteColor = { name_zh: string; name_en: string; hex: string; usage: string };
/** 一场的色彩脚本（bible_json.color_script 的一项，也是这一场色卡的设定）：场编号是 E{episode}S{scene_no}。 */
export type DramaColorScriptEntry = { episode: number; scene_no: number; scene: string; inherits_from: string; shared_colors: DramaPaletteColor[]; new_colors: DramaPaletteColor[]; purpose?: string; references?: string };
export type DramaBible = { characters?: DramaBibleCharacter[]; props?: DramaBibleProp[]; locations?: DramaBibleLocation[]; style_anchor?: DramaStyleAnchor; color_script?: DramaColorScriptEntry[]; [key: string]: unknown };

/** 场编号 E01S02（第 1 集第 2 场）。 */
export function dramaSceneCode(episode: number, sceneNo: number) {
    return `E${String(episode).padStart(2, "0")}S${String(sceneNo).padStart(2, "0")}`;
}

export function getDramaBible(projectId: string) {
    return http.get<{ document: DramaBible }>(`/projects/${projectId}/drama/bible`);
}

export function putDramaBible(projectId: string, document: DramaBible) {
    return http.put<{ document: DramaBible }>(`/projects/${projectId}/drama/bible`, { document });
}

/** 一章里的一段：derived 为 true 表示旧镜头没有分段 JSON，段号按位置合成，只能看不能拆合。 */
export type DramaSegmentView = { shotId: string; unitId: string; title: string; position: number; durationMs: number; revisionId?: string; spec: DramaSegment; derived: boolean };

export function listDramaSegments(projectId: string, unitId: string, signal?: AbortSignal) {
    return http.get<{ segments: DramaSegmentView[] }>(`/projects/${projectId}/drama/segments`, { params: { unitId }, signal });
}

/**
 * 按段号写入一章的段（后端按项目生成模型校验段长）。拆段、合段时给 order：本章全部段号的新顺序，
 * 不在其中的段连同生成结果删除；只改段内容时不给。
 */
export function writeDramaSegments(projectId: string, input: { unitId: string; segments: DramaSegment[]; order?: string[] }) {
    return http.put<{ segments: DramaSegmentView[] }>(`/projects/${projectId}/drama/segments`, input);
}

/** 镜头表门禁的一条结果：shotId 是规则指向的镜头（整表级规则为空）。 */
export type DramaCheckItem = { level: "error" | "warn"; rule: string; message: string; shotId?: string };
/** 生成分段收尾时模型输出的解析统计（一镜一行）：rate = 解析成功的行 / 像镜头行的行。 */
export type DramaShotParseStats = { lines: number; rows: number; rejected: number; rate: number };
export type DramaCheckReport = { kind: "shots"; errors: number; warnings: number; items: DramaCheckItem[]; skipped: Array<{ rule: string; why: string }>; parse?: DramaShotParseStats };
export type DramaShotlistStats = { rows: number; shots: number; no_spec?: number; line_exact?: number; line_fuzzy?: number; line_unmatched?: number; scene_remap?: number; scene_unknown?: number; dup_id?: number };
/** 一章的镜头表（契约 shotlist）、由章节正文转出的台词表与最近一次检查结果。 */
export type DramaShotlistView = {
    unitId: string;
    version: number;
    shotlist: { profile: string; episode?: number; shots: Array<Record<string, unknown>> };
    script?: Record<string, unknown>;
    check: DramaCheckReport;
    /** 检查有 error、只存了草稿还没装段：本章的段仍是上一次装出的，处理后用 repackDramaShotlist 重装。 */
    draft: boolean;
    compileProfile?: DramaCompileProfileRef;
    updatedAt: string;
};
/** profile 是这章检查、装段用的风格档案及来由（按项目画风对照）；draft 为 true 时检查有 error，只存了草稿没装段。 */
export type DramaShotlistWriteResult = { shotlist: DramaShotlistView; segments: DramaSegmentView[]; check: DramaCheckReport; stats: DramaShotlistStats; draft: boolean; notes: string[]; profile?: string };

export function getDramaShotlist(projectId: string, unitId: string, signal?: AbortSignal) {
    return http.get<{ shotlist: DramaShotlistView | null }>(`/projects/${encodeURIComponent(projectId)}/units/${encodeURIComponent(unitId)}/drama/shotlist`, { signal });
}

/** 镜头表检查不过时，从错误里取出检查结果；不是这种错误返回 undefined。 */
export function shotlistCheckFailure(error: unknown): DramaCheckReport | undefined {
    if (!(error instanceof ApiError) || error.reason !== API_ERROR_REASON.ShotlistCheckFailed) return undefined;
    const check = error.details?.check as DramaCheckReport | undefined;
    return check && Array.isArray(check.items) ? check : undefined;
}

/**
 * 用这一章已存的分镜行原文按当前生效的编译档案重新装段（不调模型），整章替换段，旧段的候选视频与证据一并删除。
 * 镜头表是检查没过的草稿时仍要过检查：还有 error 就抛 ApiError（reason=shotlist_check_failed，details.check 是检查结果），草稿不变。
 */
export function repackDramaShotlist(projectId: string, unitId: string, input: { rule?: { maxSeconds: number; minSeconds: number } } = {}) {
    return http.post<DramaShotlistWriteResult>(`/projects/${encodeURIComponent(projectId)}/units/${encodeURIComponent(unitId)}/drama/shotlist/repack`, input);
}

export function compileProjectShot(projectId: string, shotId: string, input: { model?: string } = {}) {
    return http.post<{ compiled: DramaCompileResult }>(`/projects/${projectId}/shots/${shotId}/compile`, input);
}

export function listDramaEvidence(projectId: string, signal?: AbortSignal) {
    return http.get<{ evidence: DramaEvidence[] }>(`/projects/${projectId}/drama/evidence`, { signal });
}

export function createDramaEvidence(projectId: string, input: { artifactId?: string; shotId?: string; kind: DramaEvidenceKind; passed?: boolean; note?: string; taskId?: string }) {
    return http.post<{ evidence: DramaEvidence }>(`/projects/${projectId}/drama/evidence`, input);
}

export function pickShotArtifact(projectId: string, artifactId: string, reason: string) {
    return http.post<{ artifact: ShotArtifact }>(`/projects/${projectId}/artifacts/${artifactId}/pick`, { reason });
}

export function transcribeShotArtifact(projectId: string, artifactId: string) {
    return http.post<{ task: GenerationTask }>(`/projects/${projectId}/artifacts/${artifactId}/transcribe`, {});
}

export function framesShotArtifact(projectId: string, artifactId: string) {
    return http.post<{ evidence: DramaEvidence }>(`/projects/${projectId}/artifacts/${artifactId}/frames`, {});
}

export function runDramaGate(projectId: string, input: { resourceId?: string; artifactId?: string; targetLufs?: number }) {
    return http.post<{ gate: DramaGateResult }>(`/projects/${projectId}/drama/gate`, input);
}

/** 下载交付包（zip）。服务端同时记一条「已交付」证据。 */
export async function downloadDramaDelivery(projectId: string, fallbackName = "交付包.zip") {
    let response;
    try {
        response = await http.raw<Blob>({ method: "get", url: `/projects/${projectId}/drama/delivery`, responseType: "blob" });
    } catch (error) {
        // 下载请求出错时 axios 把 JSON 错误包成 Blob，这里还原成可读的提示。
        const blob = error instanceof ApiError ? (error.cause as { response?: { data?: unknown } } | undefined)?.response?.data : undefined;
        if (blob instanceof Blob && error instanceof ApiError) {
            try {
                const envelope = JSON.parse(await blob.text()) as { msg?: string; code?: number; reason?: string };
                if (envelope?.msg) throw new ApiError(envelope.msg, { status: error.status, code: envelope.code, reason: envelope.reason, cause: error });
            } catch (parsed) {
                if (parsed instanceof ApiError) throw parsed;
            }
        }
        throw error;
    }
    const disposition = String(response.headers?.["content-disposition"] || "");
    const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1];
    const fileName = encoded ? decodeURIComponent(encoded) : fallbackName;
    const url = URL.createObjectURL(response.data);
    const link = document.createElement("a");
    link.href = url;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    return fileName;
}

export function parseEvidenceMetrics(evidence: DramaEvidence): Record<string, unknown> {
    try {
        const parsed = JSON.parse(evidence.metricsJson || "{}");
        return parsed && typeof parsed === "object" ? parsed : {};
    } catch {
        return {};
    }
}

/** yoozee 风格档案（live-realism、anime-3d …）：检查、装段的配额与规则，短剧流程每一步按它取绑定的技能。 */
export type DramaStyleProfile = { id: string; label: string };
/** 画风 → 风格档案对照表（后端 style_map.json）：medium / world 是画风选择项，keywords 匹配自定义画风的标题和标签。 */
export type DramaStyleMapRule = { label: string; medium?: string[]; world?: string[]; keywords?: string[]; profile: string };
export type DramaStyleProfiles = { profiles: DramaStyleProfile[]; default: string; styleMap: { default: string; rules: DramaStyleMapRule[] } };

export function getDramaStyleProfiles(signal?: AbortSignal) {
    return http.get<DramaStyleProfiles>("/drama/style-profiles", { signal });
}

/** 按画风对照表推荐风格档案（与后端 StyleMap.ProfileFor 同一规则：写了的条件都要满足，先写的规则先匹配）。 */
export function recommendDramaStyleProfile(styleMap: DramaStyleProfiles["styleMap"], style?: { selection?: { world?: string; medium?: string }; title?: string; tags?: string[] }) {
    const text = `${style?.title || ""} ${(style?.tags || []).join(" ")}`;
    const medium = style?.selection?.medium || "";
    const world = style?.selection?.world || "";
    const rule = styleMap.rules.find((item) => (!item.medium?.length || item.medium.includes(medium)) && (!item.world?.length || item.world.includes(world)) && (!item.keywords?.length || item.keywords.some((keyword) => text.includes(keyword))));
    return rule?.profile || styleMap.default;
}

/** 短剧流程步骤：script.adapt 是剧本优化、style.bible 是导演风格（项目级），chapter.shots 是生成分段（按章节或剧集）。 */
export type DramaStepId = "chapter.shots" | "script.adapt" | "style.bible";

/** 一个项目跑这一步用哪个技能版本（风格档案查绑定；bound 为 false 时是步骤默认技能）。 */
export type DramaStepUsage = {
    step: DramaStepId;
    stepLabel: string;
    contract: string;
    profile: DramaStyleProfile;
    skill: { id: string; name: string; versionId: string; version: string; contentHash: string };
    bound: boolean;
};

export function getDramaStepUsage(projectId: string, step: DramaStepId, signal?: AbortSignal) {
    return http.get<{ usage: DramaStepUsage }>(`/projects/${encodeURIComponent(projectId)}/drama/steps/${encodeURIComponent(step)}`, { signal });
}

/**
 * 对这一章跑这一步：提示词（技能上下文、画风、角色、资产与锚点、每集时长、契约）由服务端组装，这里只带文本模型选择。
 * 服务端在一个任务里按场续写，任务成功前解析、绑资产、写镜头表并装段（结果摘要在任务 result.dramaStep）。
 * rule 是视频渠道实际能提交的段长（只收紧）；resumeTaskId 是「从第 N 场继续」的上一次任务。
 */
export function runDramaStep(projectId: string, unitId: string, step: DramaStepId, input: { model: string; logicalModelId?: string; config: Record<string, unknown>; rule?: { maxSeconds: number; minSeconds: number }; resumeTaskId?: string }) {
    return http.post<{ task: GenerationTask; usage: DramaStepUsage }>(`/projects/${encodeURIComponent(projectId)}/units/${encodeURIComponent(unitId)}/drama/steps/${encodeURIComponent(step)}/run`, input);
}

/**
 * 剧本优化（项目级步骤）：服务端按项目记忆把所选原文章节改编成短剧剧本（mode=adapt），或把已有剧集只调节奏（mode=rhythm）；
 * 一个任务里一轮写一集，收尾写成 kind=episode 的单元（结果摘要在任务 result.dramaStep）。sourceUnitIds 不给时 adapt 取全部
 * 还没改编的原文章节、rhythm 取全部剧集；targetEpisodes / episodeSeconds 二选一。会替换已有剧集或改写有镜头表的剧集时，
 * 不带 replace 会被拒（ApiError.reason=drama_episodes_replace_required，details.episodes 列出影响面）。
 */
export type DramaScriptAdaptMode = "adapt" | "rhythm";

/**
 * 色卡出图的图片任务模板：前端按项目默认图片模型组好的任务（模型选择、input.config 等，16:9），服务端每张色卡换上自己的
 * 提示词和资产出图 metadata 后建任务。导演风格（以及剧本优化跑完自动接着的导演风格）带上它才给色卡出图。
 */
export type DramaImageTaskTemplate = { model: string; logicalModelId?: string; provider?: string; input: Record<string, unknown> };

/**
 * 导演风格（项目级步骤 style.bible）：服务端按项目记忆为全部剧集（没有剧集时是原文章节）写全局风格锚和逐场色彩脚本，一轮一集；
 * 收尾合并写进 bible_json，每场建一张色卡资产（category=palette，重跑同一场加版本），带了 paletteImages 时接着排色卡出图。
 */
export function runDramaProjectStep(
    projectId: string,
    step: "script.adapt" | "style.bible",
    input: {
        model: string;
        logicalModelId?: string;
        config: Record<string, unknown>;
        sourceUnitIds?: string[];
        mode?: DramaScriptAdaptMode;
        targetEpisodes?: number;
        episodeSeconds?: number;
        replace?: boolean;
        resumeTaskId?: string;
        paletteImages?: DramaImageTaskTemplate;
    },
) {
    return http.post<{ task: GenerationTask; usage: DramaStepUsage }>(`/projects/${encodeURIComponent(projectId)}/drama/steps/${encodeURIComponent(step)}/run`, input);
}

/** 一次排色卡出图：queued 建了任务，waiting 等空位（同时任务数有上限，前面的出完图自动接着排），failed 没建成（原因记在色卡上）。 */
export type DramaPaletteQueueResult = { queued: string[]; waiting: string[]; failed: string[] };

/** 给色卡出图 / 重新出图：assetIds 为空时排全部还没出图或出图失败的色卡。 */
export function queueDramaPaletteImages(projectId: string, input: { assetIds?: string[]; imageTask: DramaImageTaskTemplate }) {
    return http.post<DramaPaletteQueueResult>(`/projects/${encodeURIComponent(projectId)}/drama/palettes/images`, input);
}

/** 重跑剧本优化会动到的一集：adapt 整体替换（连同镜头表和段），rhythm 原地改写正文。 */
export type DramaEpisodeImpact = { unitId: string; title: string; shots: number; hasShotlist: boolean };

export function dramaEpisodesReplaceImpact(error: unknown): { mode: DramaScriptAdaptMode; episodes: DramaEpisodeImpact[] } | undefined {
    if (!(error instanceof ApiError) || error.reason !== API_ERROR_REASON.DramaEpisodesReplace) return undefined;
    const episodes = error.details?.episodes as DramaEpisodeImpact[] | undefined;
    return Array.isArray(episodes) ? { mode: error.details?.mode === "rhythm" ? "rhythm" : "adapt", episodes } : undefined;
}

/** 一集剧集的来源：原文章节的第 from 到第 to 段（含首尾）。 */
export type DramaEpisodeSource = { unitId: string; from: number; to: number };

/** 项目的剧集、各自来源（剧集正文开头的来源注释）和已有的段，以及已被剧集引用的原文章节。 */
export type DramaEpisodes = {
    episodes: Array<{ unitId: string; title: string; parentId?: string; position: number; wordCount: number; sources: DramaEpisodeSource[]; shots: number; hasShotlist: boolean }>;
    adaptedChapterIds: string[];
};

export function dramaEpisodesKey(projectId: string) {
    return ["project", projectId, "drama-episodes"] as const;
}

export function getDramaEpisodes(projectId: string, signal?: AbortSignal) {
    return http.get<DramaEpisodes>(`/projects/${encodeURIComponent(projectId)}/drama/episodes`, { signal });
}

/** 剧本优化收尾的摘要（任务 result.dramaStep）：写出的各集、替换或改写了哪些已有剧集、检查提醒。 */
export type DramaScriptAdaptOutcome = {
    step: "script.adapt";
    status: "written";
    mode: DramaScriptAdaptMode;
    episodes: Array<{ unitId: string; no: number; title: string; parentId?: string; sources: DramaEpisodeSource[]; scenes: number }>;
    replaced?: string[];
    rewritten?: string[];
    parse: { blocks: number; episodes: number; rate: number };
    errors: number;
    warnings: number;
    items?: DramaCheckReport["items"];
    /** 写完剧集后自动接着建的导演风格任务；error 是没建成的原因（已写的剧集不受影响）。 */
    chained?: { step: "style.bible"; taskId?: string; error?: string };
};

/** 导演风格收尾的摘要（任务 result.dramaStep）：写了几集几场、色卡新建 / 加版本几张、出图排上和等空位的色卡。 */
export type DramaStyleBibleOutcome = {
    step: "style.bible";
    status: "written";
    episodes: number;
    scenes: number;
    parse: { episodes: number; scenes: number; invalid: number; anchor: boolean };
    errors: number;
    warnings: number;
    items?: DramaCheckReport["items"];
    palettes: { created: number; versioned: number };
    images: DramaPaletteQueueResult;
};

/** 逐轮续写的进度（任务 result.dramaRun）：completed / total 场（剧本优化是集）；任务失败时前面写完的仍在，可从第 completed + 1 场继续。 */
export type DramaRunProgress = {
    runId: string;
    total: number;
    completed: number;
    scenes?: Array<{ scene: string; heading: string; opening: string }>;
    turns: Array<{ index: number; scene?: string; heading?: string; rows: number; attempts: number; taskId: string }>;
};

/** 生成分段收尾的摘要（任务 result.dramaStep）：written 已写镜头表并装段；draft 检查有 error，只存了草稿。 */
export type DramaStepOutcome = {
    step: "chapter.shots";
    status: "written" | "draft";
    unitId: string;
    parse: DramaShotParseStats;
    errors: number;
    warnings: number;
    shots: number;
    segments: number;
    scenes: number;
    version: number;
    notes?: string[];
    profile?: string;
};

function taskResultRecord(task: Pick<GenerationTask, "resultJson">): Record<string, unknown> {
    try {
        const parsed = JSON.parse(task.resultJson || "{}") as unknown;
        return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : {};
    } catch {
        return {};
    }
}

export function dramaRunProgressOf(task: Pick<GenerationTask, "resultJson">): DramaRunProgress | undefined {
    const run = taskResultRecord(task).dramaRun as DramaRunProgress | undefined;
    return run && typeof run.total === "number" && typeof run.completed === "number" ? run : undefined;
}

export function dramaStepOutcomeOf(task: Pick<GenerationTask, "resultJson">): DramaStepOutcome | undefined {
    const outcome = taskResultRecord(task).dramaStep as DramaStepOutcome | undefined;
    return outcome && outcome.step === "chapter.shots" && (outcome.status === "written" || outcome.status === "draft") ? outcome : undefined;
}

export function dramaScriptAdaptOutcomeOf(task: Pick<GenerationTask, "resultJson">): DramaScriptAdaptOutcome | undefined {
    const outcome = taskResultRecord(task).dramaStep as DramaScriptAdaptOutcome | undefined;
    return outcome && outcome.step === "script.adapt" && Array.isArray(outcome.episodes) ? outcome : undefined;
}

export function dramaStyleBibleOutcomeOf(task: Pick<GenerationTask, "resultJson">): DramaStyleBibleOutcome | undefined {
    const outcome = taskResultRecord(task).dramaStep as DramaStyleBibleOutcome | undefined;
    return outcome && outcome.step === "style.bible" && typeof outcome.scenes === "number" ? outcome : undefined;
}
