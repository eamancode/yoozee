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
    /** 按小节生成（M8）：这一段对应的小节（一节一段，E01S02-P1）和戏类（wenxi15 / wenxi30 / wuxi）；旧镜头表装出的段没有。 */
    section?: string;
    genre?: DramaShotGenreId;
};

/** 戏类：文戏 15（7.3 规则书）、文戏 30（即梦小节分镜）、武戏（预留，卡 E）。按项目视频模型单段上限选文戏 15 / 30。 */
export type DramaShotGenreId = "wenxi15" | "wenxi30" | "wuxi";

const dramaShotGenreLabels: Record<string, string> = { wenxi15: "文戏 15", wenxi30: "文戏 30", wuxi: "武戏" };

/** 戏类的中文名；不认识的原样返回。 */
export function dramaShotGenreLabel(genre?: string) {
    return genre ? dramaShotGenreLabels[genre] || genre : "";
}

/** 生成分段这次用的戏类：maxSeconds 是一节的上限（戏类口径与项目视频模型单段上限取小），model 是视频模型名。 */
export type DramaStepGenre = { id: DramaShotGenreId; label: string; maxSeconds: number; model: string };

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
    /** 带问题装段后挂在这一段镜头上的镜头表检查 error 条数。 */
    issues?: number;
};

export type DramaEndState = { key: string; label: string; done: boolean; detail: string; skill: string };

/** 视频前的预演闸门（brief_json.previz_gate）：auto 跟授权档位（每步确认强制、全权跳过）。 */
export type DramaPrevizGate = "auto" | "on" | "off";

export type DramaProgress = {
    model: DramaModel;
    authMode: DramaAuthMode | "";
    /** 预演闸门是否生效：生效时没有选中的黑白预演不能生成视频（后端拒绝，reason previz_required）。 */
    previzGate: boolean;
    previzGateSetting: DramaPrevizGate;
    segments: DramaSegmentProgress[];
    /** issues 是带问题的段数（带问题装段后还没改完的）。 */
    counts: { segments: number; script: number; refs: number; generated: number; takes: number; reviewed: number; inCut: number; accepted: number; overLimit: number; issues?: number };
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
/** 全局风格锚（bible_json.style_anchor：建项目时风格锁定 style.lock 写入；场次色卡只读不改）。 */
export type DramaStyleAnchor = { medium?: string; art_style?: string; lighting?: string; camera?: string; grading?: string; forbidden?: string[]; era?: string; genre?: string; render_quality?: string };
/** 色板里的一个颜色；group 是场次色卡里这个颜色归主基调色卡的哪一组（主色卡的颜色已经按组放好，不写）。 */
export type DramaPaletteColor = { name_zh: string; name_en: string; hex: string; usage: string; group?: DramaMasterPaletteGroupKey | string };
/** 一场的色彩脚本（bible_json.color_script 的一项，也是这一场场次色卡的设定）：场编号是 E{episode}S{scene_no}。 */
export type DramaColorScriptEntry = {
    episode: number;
    scene_no: number;
    scene: string;
    inherits_from: string;
    shared_colors: DramaPaletteColor[];
    new_colors: DramaPaletteColor[];
    purpose?: string;
    references?: string;
    unit_id?: string;
    scene_rule?: number;
};
/** 主基调色卡的分组键：主角组、配角组、道具组、环境组；认不出的分组是 other。 */
export type DramaMasterPaletteGroupKey = "protagonist" | "supporting" | "props" | "environment" | "other";
/** 主基调色卡（bible_json.master_palette，也随风格存进平台风格库）：按分组的色板。 */
export type DramaMasterPalette = { groups: Array<{ key: DramaMasterPaletteGroupKey; name: string; colors: DramaPaletteColor[] }> };
/** 项目锁定风格的来源（bible_json.style_lock）：analysis 风格分析后锁定，library 取自平台风格库，preset 按画风当场生成。 */
export type DramaStyleLockRecord = {
    source: "analysis" | "library" | "preset";
    library_id?: string;
    project_type?: string;
    task_id?: string;
    asset_id?: string;
    resource_id?: string;
    locked_at?: string;
    locked_by?: string;
};
export type DramaBible = {
    characters?: DramaBibleCharacter[];
    props?: DramaBibleProp[];
    locations?: DramaBibleLocation[];
    style_anchor?: DramaStyleAnchor;
    color_script?: DramaColorScriptEntry[];
    master_palette?: DramaMasterPalette;
    style_lock?: DramaStyleLockRecord;
    [key: string]: unknown;
};

/** 主基调色卡的全部颜色（按分组顺序）。 */
export function masterPaletteColors(palette?: DramaMasterPalette) {
    return (palette?.groups || []).flatMap((group) => group.colors || []);
}

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

/** 一章里的一段：derived 为 true 表示旧镜头没有分段 JSON，段号按位置合成，只能看不能拆合；issues 是带问题装段后挂在这一段镜头上的检查 error。 */
export type DramaSegmentView = { shotId: string; unitId: string; title: string; position: number; durationMs: number; revisionId?: string; spec: DramaSegment; derived: boolean; issues?: DramaShotIssue[] };

export function listDramaSegments(projectId: string, unitId: string, signal?: AbortSignal) {
    return http.get<{ segments: DramaSegmentView[] }>(`/projects/${projectId}/drama/segments`, { params: { unitId }, signal });
}

/**
 * 按段号写入一章的段（后端按项目生成模型校验段长）。拆段、合段时给 order：本章全部段号的新顺序，
 * 不在其中的段连同生成结果删除；只改段内容时不给。inherit 是「段号 → 从哪几段继承资产引用」：
 * 拆段时新段继承原段，合段、挪镜头时接收的段并上来源段（后端按版本和用途去重）。
 */
export function writeDramaSegments(projectId: string, input: { unitId: string; segments: DramaSegment[]; order?: string[]; inherit?: Record<string, string[]>; shots?: DramaShot[]; shotlistVersion?: number }) {
    return http.put<{ segments: DramaSegmentView[]; check?: DramaSegmentsCheck }>(`/projects/${projectId}/drama/segments`, input);
}

/**
 * 镜头表的一镜（契约 shotlist）：分镜卡编辑改的是它。这里只列分镜卡会改的字段，其余字段原样带回。
 * size 只能取镜头表的景别，emotion_arc.id 只能取情绪弧索引里的 id，est 是这一镜的秒数。
 */
export type DramaShotLine = { line_id?: string; delivery?: string; offscreen?: boolean; pace?: string; tone?: string; emotion?: string; placement?: string; text?: string; speaker?: string };
export type DramaShotBlocking = { who: string; at: string; facing?: string; behind?: string; occluded_by?: string };
export type DramaShotLight = { source_anchor?: string; direction?: string; policy?: string; note?: string; key?: string; fill?: string; rim?: string; ratio?: string; kelvin?: string; shadow?: string; exposure?: string; change?: string };
export type DramaShot = {
    id: string;
    scene: string;
    action?: string;
    composition?: string;
    size?: string;
    est?: number;
    cam?: { z?: string; y?: string; x?: string; f?: Record<string, unknown> };
    move?: string[] | string;
    camera_side?: string;
    blocking?: DramaShotBlocking[];
    light?: DramaShotLight;
    lines?: DramaShotLine[];
    emotion_arc?: { id: string; phase_from?: number; phase_to?: number; continues?: string; intensity?: string; trigger?: string };
    /** 按小节生成时这一镜所在的小节、戏类，和技能原生输出里统一镜头表没有对应字段的标签（风格、核心情绪、场景锚点等）。 */
    section?: string;
    genre?: DramaShotGenreId;
    extras?: Record<string, string>;
    [key: string]: unknown;
};

/** 分镜卡编辑写段后整章重检的结果：accepted 表示还有 error、段照样带着问题。 */
export type DramaSegmentsCheck = { version: number; errors: number; warnings: number; accepted: boolean };

/**
 * 镜头表检查的一条问题挂到哪几镜上：shots 是镜头表 id；scene 是所在场，section 是所在小节（「重新生成问题小节」按它挑节）。
 * fix 是台词类快捷修复：add_line 把 lineId 补进这一镜，keep_line 只保留在这一镜（其余镜头去掉）。
 */
export type DramaShotIssue = DramaCheckItem & { scene?: string; section?: string; shots?: string[]; fix?: "add_line" | "keep_line" };

/** 镜头表门禁的一条结果：shotId 是规则指向的镜头（整表级规则为空）。 */
export type DramaCheckItem = { level: "error" | "warn"; rule: string; message: string; shotId?: string; lineId?: string };
/** 生成分段收尾时模型输出的解析统计（一镜一行）：rate = 解析成功的行 / 像镜头行的行。 */
export type DramaShotParseStats = { lines: number; rows: number; rejected: number; rate: number };
export type DramaCheckReport = { kind: "shots"; errors: number; warnings: number; items: DramaCheckItem[]; skipped: Array<{ rule: string; why: string }>; parse?: DramaShotParseStats; accepted?: boolean };
export type DramaShotlistStats = { rows: number; shots: number; no_spec?: number; line_exact?: number; line_fuzzy?: number; line_unmatched?: number; scene_remap?: number; scene_unknown?: number; dup_id?: number };
/** 一章的镜头表（契约 shotlist）、由章节正文转出的台词表与最近一次检查结果。 */
export type DramaShotlistView = {
    unitId: string;
    version: number;
    shotlist: { profile: string; episode?: number; shots: DramaShot[] };
    script?: DramaScript;
    check: DramaCheckReport;
    /** 检查有 error、只存了草稿还没装段：本章的段仍是上一次装出的，处理后用 repackDramaShotlist 重装。 */
    draft: boolean;
    /** 检查有 error、但已带着问题装段（「先装段，到分镜制作改」）：问题挂在段和镜头上。 */
    accepted?: boolean;
    /** 检查的 error 挂到哪几镜、哪一场、能不能一键修。 */
    issues?: DramaShotIssue[];
    compileProfile?: DramaCompileProfileRef;
    /** 按小节生成时每一节的原样输出（技能原生格式，含 BGM 等不进镜头表的内容）、转换统计和转出的镜头；旧镜头表没有。 */
    sections?: DramaSectionOutput[];
    updatedAt: string;
};

/** 一节原样输出里读出的小节级字段（7.3 的要素提取、当前空间站位，即梦的小节头和小节尾）。 */
export type DramaSectionField = { name: string; value: string };
/** 一节的转换统计：lineMerged 是一句拆在相邻两镜合回第一镜的，lineDuplicate 是落了不相邻两镜的，lineMissing 是没写到的，noLight 是室内补不出光源锚点的。 */
export type DramaSectionConvertStats = {
    blocks: number;
    shots: number;
    rejected?: number;
    lines: number;
    lineUnmatched?: number;
    lineMerged?: number;
    lineDuplicate?: number;
    lineMissing?: number;
    noLight?: number;
    musicStripped?: number;
    noSize?: number;
};
/** 按小节生成时一节的原样输出（drama_shotlists.sections_json 的一项）。 */
export type DramaSectionOutput = {
    id: string;
    scene: string;
    heading: string;
    no: number;
    genre: DramaShotGenreId;
    lines: [number, number];
    lineIds: string[];
    est: number;
    raw: string;
    fields?: DramaSectionField[];
    stats: DramaSectionConvertStats;
    shots: string[];
    taskId?: string;
    /** 切节时没满足戏类口径的提醒（如文戏 15 连着接话超过两句台词），只是 warn。 */
    warnings?: string[];
};
/** profile 是这章检查、装段用的风格档案及来由（按项目画风对照）；draft 为 true 时检查有 error，只存了草稿没装段。 */
export type DramaShotlistWriteResult = { shotlist: DramaShotlistView; segments: DramaSegmentView[]; check: DramaCheckReport; stats: DramaShotlistStats; draft: boolean; accepted?: boolean; notes: string[]; profile?: string };

/** 由章节正文转出的台词表（契约 script）：kind 为 line / vo 的节拍是台词，id 是镜头里引用的 line_id。 */
export type DramaScript = { title?: string; scenes: Array<{ id: string; heading: string; beats: Array<{ id: string; kind: string; speaker?: string; text: string }> }> };

/** 分镜卡编辑能选的值：景别、机位（cam.x / cam.y）、运镜（键是镜头表写法，值是编译档案的叫法），情绪弧只能从索引里选。 */
export type DramaShotVocab = { sizes: string[]; cameraX: Record<string, string>; cameraY: Record<string, string>; moves: Record<string, string>; emotionArcs: Array<{ id: string; name: string; family: string; phases: string[] }> };

export function getDramaShotVocab(signal?: AbortSignal) {
    return http.get<{ vocab: DramaShotVocab }>("/drama/shot-vocab", { signal });
}

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
 * acceptIssues 是「先装段，到分镜制作改」：带着 error 装段，问题挂到段和镜头上；会让装段做不成的问题（镜头时长不是正数、整场超单段上限）仍拒绝。
 */
export function repackDramaShotlist(projectId: string, unitId: string, input: { rule?: DramaSegmentRule; acceptIssues?: boolean } = {}) {
    return http.post<DramaShotlistWriteResult>(`/projects/${encodeURIComponent(projectId)}/units/${encodeURIComponent(unitId)}/drama/shotlist/repack`, input);
}

/**
 * 把一段编译成项目生成模型的提示词。maxReferenceImages 是这次要用的视频模型最多带几张参考图（按渠道模型能力配置）：本场色卡图排在
 * 角色 / 场景 / 道具之后，放不下时不带图、色值写进提示词（结果 issues 里有一条 warn）。
 */
export function compileProjectShot(projectId: string, shotId: string, input: { model?: string; maxReferenceImages?: number } = {}) {
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

/** 短剧流程步骤：style.lock 是风格锁定、script.adapt 是剧本优化、scene.palette 是场次色卡（项目级），assets.extract 是资产设计、chapter.shots 是生成分段（按章节或剧集）。 */
export type DramaStepId = "chapter.shots" | "style.lock" | "script.adapt" | "scene.palette" | "assets.extract";

/** 一个项目跑这一步用哪个技能版本（风格档案查绑定；bound 为 false 时是步骤默认技能）。 */
export type DramaStepUsage = {
    step: DramaStepId;
    stepLabel: string;
    contract: string;
    profile: DramaStyleProfile;
    skill: { id: string; name: string; versionId: string; version: string; contentHash: string };
    bound: boolean;
    /** 生成分段这次用的戏类（按项目视频模型单段上限选）和绑定表里实际查的那一格（chapter.shots#wenxi15）；其余步骤没有。 */
    genre?: DramaStepGenre;
    bindingStep?: string;
};

/** 视频渠道实际能提交的段长（只收紧）：生成分段按它选戏类、切小节、装段。 */
export type DramaSegmentRule = { maxSeconds: number; minSeconds: number };

/** 步骤用法、场次表带上与生成分段同一个 rule，弹窗的戏类和场次条的节数才与实际运行一致。 */
function dramaRuleParams(rule?: DramaSegmentRule) {
    return rule ? { maxSeconds: rule.maxSeconds, minSeconds: rule.minSeconds } : undefined;
}

/** 这一步用的风格档案和技能；生成分段要带上运行时同一个 rule（见 dramaRuleParams）。 */
export function getDramaStepUsage(projectId: string, step: DramaStepId, signal?: AbortSignal, rule?: DramaSegmentRule) {
    return http.get<{ usage: DramaStepUsage }>(`/projects/${encodeURIComponent(projectId)}/drama/steps/${encodeURIComponent(step)}`, { params: dramaRuleParams(rule), signal });
}

/**
 * 对这一章跑这一步：提示词由服务端组装，这里只带文本模型选择。服务端在一个任务里按场续写，任务成功前写库（结果摘要在任务 result.dramaStep）：
 * 生成分段解析、绑资产、写镜头表并装段，rule 是视频渠道实际能提交的段长（只收紧）；资产设计写已确认资产，带了 imageTask 时接着给新资产排出图。
 * resumeTaskId 是「从第 N 节继续」的上一次任务；onlySections 是生成分段的「重新生成问题小节」：只重写这几节，其余节用镜头表里已有的镜头行、
 * 原样输出进对话历史；onlyScenes 等于重写这几场的全部小节。
 */
export function runDramaStep(
    projectId: string,
    unitId: string,
    step: "chapter.shots" | "assets.extract",
    input: { model: string; logicalModelId?: string; config: Record<string, unknown>; rule?: DramaSegmentRule; resumeTaskId?: string; onlyScenes?: string[]; onlySections?: string[]; imageTask?: DramaImageTaskTemplate },
) {
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
 * 资产出图的图片任务模板：前端按项目默认图片模型组好的任务（模型选择、input.config 等），服务端每个资产换上自己的
 * 提示词和资产出图 metadata 后建任务。资产设计带上它才给新资产出图。
 */
export type DramaImageTaskTemplate = { model: string; logicalModelId?: string; provider?: string; input: Record<string, unknown> };

/**
 * 项目级步骤：风格锁定（style.lock，建项目时按类型、风格文字、参考图和画风出风格锚和主基调色卡，收尾只出候选，lockOnFinish 时直接锁定）、
 * 剧本优化（script.adapt，写完剧集服务端自动接着建场次色卡任务）、场次色卡（scene.palette：项目锁定风格后，以风格锚和主基调色卡为参考
 * 为全部剧集逐场写色彩脚本，一轮一集，收尾写进 bible_json.color_script、每场建 / 加版本一张场次色卡，色卡图由代码合成）。
 */
export function runDramaProjectStep(
    projectId: string,
    step: "style.lock" | "script.adapt" | "scene.palette",
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
        /** 风格锁定：项目类型（live-action …）、风格文字、参考图资源 ID（最多 6 张）、收尾直接锁定。 */
        projectType?: string;
        styleText?: string;
        referenceImages?: string[];
        lockOnFinish?: boolean;
    },
) {
    return http.post<{ task: GenerationTask; usage: DramaStepUsage }>(`/projects/${encodeURIComponent(projectId)}/drama/steps/${encodeURIComponent(step)}/run`, input);
}

/** 一次排出图：queued 建了任务，waiting 等空位（同时任务数有上限，前面的出完图自动接着排），failed 没建成（原因记在资产上）。 */
export type DramaImageQueueResult = { queued: string[]; waiting: string[]; failed: string[] };

/** 重新合成场次色卡的结果：rendered 是合成并绑上图的色卡，failed 是没合成上的（带原因）。 */
export type DramaPaletteResynthesizeResult = { rendered: string[]; failed: Array<{ assetId: string; code: string; error: string }> };

/** 资产页「重新合成」：按色卡当前版本的色板由代码重画图、绑成新版本（镜头引用跟过去），不调模型；assetIds 为空时只画还没有图的。 */
export function resynthesizeDramaPalettes(projectId: string, input: { assetIds?: string[] } = {}) {
    return http.post<DramaPaletteResynthesizeResult>(`/projects/${encodeURIComponent(projectId)}/drama/palettes/resynthesize`, input);
}

/** 资产页「继续出图」：assetIds 为空时排资产设计建的、还没有图也没在出图的角色（三视图）、场景和道具（概念图）。 */
export function queueDramaAssetImages(projectId: string, input: { assetIds?: string[]; imageTask: DramaImageTaskTemplate }) {
    return http.post<DramaImageQueueResult>(`/projects/${encodeURIComponent(projectId)}/drama/assets/images`, input);
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

/** 正文里的一个场次头：line 是正文第几个非空行（从 1 数）。 */
export type DramaSceneHeading = { text: string; inOut?: string; location?: string; time?: string; line: number };

/** 场次表里的一场（同一集里连续同地点的场次头并成一场）。id 是场次色卡用的场编号（E 后是集号），shotScene 是镜头 spec.scene 用的单元内编号。 */
export type DramaScene = {
    id: string;
    shotScene: string;
    no: number;
    heading: string;
    inOut?: string;
    location?: string;
    times: string[];
    sourceHeadings: DramaSceneHeading[];
    /** 正文行范围 [首行, 末行]（只数非空行，从 1 数）。 */
    lines: [number, number];
    lineCount: number;
    /** 规则改变前（一个场次头一场）的场序。 */
    legacyNos: number[];
    /** 这一场按戏类切出的小节（一节 = 一次生成 = 一段视频）。 */
    sections?: DramaSceneSection[];
};

/** 场次表里的一个小节：id 是小节编号（E01S02-P1），lines 是覆盖的正文行，est 是估时秒数；warnings 是切节没满足戏类口径的地方。 */
export type DramaSceneSection = { id: string; no: number; genre: DramaShotGenreId; lines: [number, number]; lineIds: string[]; speakers: string[]; est: number; warnings?: string[] };

/** 场次规则改变后编号错位的已有数据。 */
export type DramaSceneRuleNotice = { legacyScenes: number; colorScript: string[]; palettes: string[]; shotlist: boolean; message: string };

/** 一个单元的场次表：episode 是场次色卡用的集号，0 表示不在项目场次表里（已有剧集时的原文章节）。 */
export type DramaScenes = {
    unitId: string;
    title: string;
    episode: number;
    fromChapters: boolean;
    rule: number;
    scenes: DramaScene[];
    notice?: DramaSceneRuleNotice;
    /** 生成分段按哪个戏类切小节（项目视频模型单段上限 ≤15 秒文戏 15，更长文戏 30）。 */
    genre?: DramaStepGenre;
};

export function dramaScenesKey(projectId: string, unitId?: string) {
    return unitId ? (["project", projectId, "drama-scenes", unitId] as const) : (["project", projectId, "drama-scenes"] as const);
}

/** 一个单元的场次表（按正文实时切）；每场的小节按 rule 选的戏类切，与生成分段同口径。 */
export function getDramaScenes(projectId: string, unitId: string, signal?: AbortSignal, rule?: DramaSegmentRule) {
    return http.get<DramaScenes>(`/projects/${encodeURIComponent(projectId)}/units/${encodeURIComponent(unitId)}/drama/scenes`, { params: dramaRuleParams(rule), signal });
}

/** 项目场次表里每个单元（全部剧集，没有剧集时全部原文章节）的场次表。 */
export function getProjectDramaScenes(projectId: string, signal?: AbortSignal, rule?: DramaSegmentRule) {
    return http.get<{ units: DramaScenes[] }>(`/projects/${encodeURIComponent(projectId)}/drama/scenes`, { params: dramaRuleParams(rule), signal });
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
    /** 写完剧集后服务端自动接着建的场次色卡任务；error 是没建成的原因（如项目还没锁定风格），剧集照写。 */
    chained?: { step: "scene.palette"; taskId?: string; error?: string };
};

/** 场次色卡收尾的摘要（任务 result.dramaStep）：写了几集几场、色卡新建 / 加版本几张、合成了几张图、没合成上的。 */
export type DramaScenePaletteOutcome = {
    step: "scene.palette";
    status: "written";
    episodes: number;
    scenes: number;
    parse: { episodes: number; scenes: number; invalid: number; anchorIgnored?: number };
    errors: number;
    warnings: number;
    items?: DramaCheckReport["items"];
    /** retired 是这次运行范围内场次已不存在、标成过期的旧色卡（不删）。 */
    palettes: { created: number; versioned: number; retired?: number };
    rendered: number;
    imageFailures?: Array<{ assetId: string; code: string; error: string }>;
};

/** 风格锁定收尾的结果（任务 result.dramaStep）：candidate 是候选（等用户点「锁定此风格」），locked 是建项目时只选了画风、收尾已直接锁定。 */
export type DramaStyleLockOutcome = {
    step: "style.lock";
    status: "candidate" | "locked";
    source: "analysis" | "preset";
    projectType: string;
    styleAnchor: DramaStyleAnchor;
    masterPalette: DramaMasterPalette;
    parse: { groups: number; colors: number; invalid: number; anchor: boolean };
    errors: number;
    warnings: number;
    items?: DramaCheckReport["items"];
    locked?: DramaStyleLockRecord;
};

/** 资产设计写出的一个资产：created 新建，updated 已有资产补了别名或外观，merged 已有、没有可补的。 */
export type DramaAssetsOutcomeItem = { assetId: string; name: string; category: "character" | "environment" | "prop"; action: "created" | "updated" | "merged" };
export type DramaAssetsCategoryCount = { created: number; merged: number; skipped: number };

/** 资产设计收尾的摘要（任务 result.dramaStep）：新增 / 合并 / 跳过（按分类）、写出的资产、出图排上和等空位的资产。 */
export type DramaAssetsExtractOutcome = {
    step: "assets.extract";
    status: "written";
    unitId: string;
    parse: { rows: number; invalid: number };
    created: number;
    merged: number;
    skipped: number;
    byCategory: Partial<Record<"character" | "environment" | "prop", DramaAssetsCategoryCount>>;
    errors: number;
    warnings: number;
    items?: DramaCheckReport["items"];
    assets: DramaAssetsOutcomeItem[];
    images: DramaImageQueueResult;
};

/** 逐轮续写的进度（任务 result.dramaRun）：completed / total 场（剧本优化是集）；任务失败时前面写完的仍在，可从第 completed + 1 场继续。 */
export type DramaRunProgress = {
    runId: string;
    total: number;
    completed: number;
    scenes?: Array<{ scene: string; heading: string; opening: string; section?: string; genre?: DramaShotGenreId; est?: number }>;
    turns: Array<{ index: number; scene?: string; heading?: string; section?: string; rows: number; attempts: number; taskId: string; kept?: boolean }>;
    /** 按小节生成时这次运行的戏类。 */
    genre?: DramaShotGenreId;
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
    /** 按小节生成时的戏类和写了几节。 */
    genre?: DramaShotGenreId;
    sections?: number;
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

export function dramaAssetsExtractOutcomeOf(task: Pick<GenerationTask, "resultJson">): DramaAssetsExtractOutcome | undefined {
    const outcome = taskResultRecord(task).dramaStep as DramaAssetsExtractOutcome | undefined;
    return outcome && outcome.step === "assets.extract" && Array.isArray(outcome.assets) ? outcome : undefined;
}

export function dramaStyleLockOutcomeOf(task: Pick<GenerationTask, "resultJson">): DramaStyleLockOutcome | undefined {
    const outcome = taskResultRecord(task).dramaStep as DramaStyleLockOutcome | undefined;
    return outcome && outcome.step === "style.lock" && outcome.masterPalette && Array.isArray(outcome.masterPalette.groups) ? outcome : undefined;
}

export function dramaScenePaletteOutcomeOf(task: Pick<GenerationTask, "resultJson">): DramaScenePaletteOutcome | undefined {
    const outcome = taskResultRecord(task).dramaStep as DramaScenePaletteOutcome | undefined;
    return outcome && outcome.step === "scene.palette" && typeof outcome.scenes === "number" ? outcome : undefined;
}
