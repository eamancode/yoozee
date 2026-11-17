/**
 * 短剧新工作流的无模型接口夹具：组件生命周期回归（happy-dom）和真实路由 Chrome E2E 共用。
 *
 * 只模拟读接口和分镜保存（PUT /drama/segments）。不模拟任何生成接口：生成、出图、出片、
 * 色卡、提取资产等写请求一律记进 forbidden 并返回 403，测试据此断言「没有提交生成」。
 * 没列出的读接口返回 404 并记进 unknown，不伪造数据。
 */

import { defaultModelCapabilityConfig } from "../../src/lib/model-capabilities";
import type { ProjectVideoOverride, ProjectVideoRoute } from "../../src/services/api/project-video-routing";

export const FIXTURE_PROJECT_ID = "p-flow-qa";
export const FIXTURE_UNIT_ID = "u-flow-e1";
export const FIXTURE_SHOT_ID = "shot-s01";
export const FIXTURE_SHOT_ID_2 = "shot-s02";
export const FIXTURE_ASSET_ID = "asset-linshen";
export const FIXTURE_ASSET_ID_2 = "asset-apo";
export const FIXTURE_VIDEO_MODEL = "CHANNEL_000001::lec-vp-seedance-2-5-f";
export const FIXTURE_VIDEO_MODEL_NAME = "Seedance 2.5 Fast";
export const FIXTURE_IMAGE_PORTRAIT = "res-linshen-portrait";
export const FIXTURE_IMAGE_SQUARE = "res-market-square";
// 夹具图片（base64 PNG）：竖版 90×160 和方形 120×120 的纯色图。
const FIXTURE_PNG_PORTRAIT =
    "iVBORw0KGgoAAAANSUhEUgAAAFoAAACgCAIAAAADw+wqAAAA50lEQVR4nO3QMQ0AIADAMIQhDInI4kMC42gyAUvHXlO3kR98FQ4cOHDgwIEDBw4cfThw4MCBAwcOHDhw9OHAgQMHDhw4cODA0YcDBw4cOHDgwIEDRx8OHDhw4MCBAwcOHH04cODAgQMHDhw4cPThwIEDBw4cOHDgwNGHAwcOHDhw4MCBA0cfDhw4cODAgQMHDhx9OHDgwIEDBw4cOHD04cCBAwcOHDhw4MDRhwMHDhw4cODAgQNHHw4cOHDgwIEDBw4cfThw4MCBAwcOHDhw9OHAgQMHDhw4cODA0YcDBw4cOHDgwPGqAyifg95oCoAaAAAAAElFTkSuQmCC";
const FIXTURE_PNG_SQUARE =
    "iVBORw0KGgoAAAANSUhEUgAAAHgAAAB4CAIAAAC2BqGFAAAA60lEQVR4nO3QAQkAIADAMIMZzIjGsoXCHTzA2Zhr60Lj+cEngQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtwINuhVo0K1Ag24FGnQr0KBbgQbdCjToVqBBtzpQ+4PevierUgAAAABJRU5ErkJggg==";

export type StyleFixture = "none" | "anchor_only" | "locked";

type Envelope = { code: number; data?: unknown; msg: string; reason?: string; details?: Record<string, unknown> };
/** binary / contentType：二进制响应（资源文件 PNG，base64），E2E 按 contentType 回；JSON 接口不用。 */
export type FixtureResponse = { status: number; body: Envelope | null; binary?: string; contentType?: string };
export type FixtureCall = { method: string; path: string };

const now = "2026-11-12T08:00:00Z";
const STYLE_ANCHOR = { medium: "写实电影感", lighting: "雨夜冷暖对比", palette_note: "冷蓝底色、暖橙点缀" };
const MASTER_PALETTE = { groups: [{ name: "主色", colors: [{ hex: "#1f3a5f", name: "雨夜蓝" }] }] };

function bibleFor(style: StyleFixture) {
    if (style === "none") return {};
    if (style === "anchor_only") return { style_anchor: STYLE_ANCHOR };
    return { style_anchor: STYLE_ANCHOR, master_palette: MASTER_PALETTE, style_lock: { source: "analysis" } };
}

function segmentSpec(id: string, text: string, dur = 15) {
    return { id, scene: "E01S01", title: `${id} 随行鲜`, dur, mode: "text_only" as const, shots: [{ at: 0, size: "中景", camera: "固定机位", text, lines: [{ speaker: "阿婆", text: "喝口热的。" }], sound: "棚外暴雨声" }] };
}

/** 某个接口这一次怎么答：一直不回（在读）或失败。按路径（不含查询串）匹配，测试中途可以改。 */
/** capability / capability-bare：按后端格式回 400 model_capability_not_supported，前者带 details，后者不带。 */
export type FixtureOverride = "pending" | "error" | "capability" | "capability-bare";

/**
 * 夹具视频模型的权威能力（和后端一样以它为准）：只有 15/30 秒两档、不支持同步音频和水印、只有 720p。
 * 预览请求会按它校验真实发出的配置，不符合就回 400，不再无论配置如何都给成功预览。
 */
// 和真实模型目录一样给完整的能力配置（前端对目录里的配置不做缺字段补齐），只改相关字段。
export const FIXTURE_VIDEO_CAPABILITY = (() => {
    const config = defaultModelCapabilityConfig(undefined, "lec-vp-seedance-2-5-f");
    const video = config.video!;
    video.duration = { ...video.duration, selection: "enum", values: [15, 30], default: 15 };
    video.generateAudio = { supported: false, default: false };
    video.watermark = { supported: false, default: false };
    video.ratios = ["9:16", "16:9"];
    video.defaultRatio = "9:16";
    video.resolutions = ["720p"];
    video.defaultResolution = "720p";
    return config;
})();

function longSegmentSpec(id: string, firstText: string, count: number) {
    const spec = segmentSpec(id, firstText);
    const extra = Array.from({ length: count - 1 }, (_, index) => ({ at: (index + 1) * 1.5, size: "近景", camera: "缓推", text: `第 ${index + 2} 镜：雨水顺着棚檐落下，林深握紧橙汁杯。`, lines: [], sound: "雨声" }));
    return { ...spec, shots: [{ ...spec.shots[0], at: 0 }, ...extra] };
}

/**
 * 出片链列表夹具：none 没有链；history 一条已完成的历史链；failed S01 出片失败；running S01 正在出。
 * 都只读：查看、预览可以，创建、重试、取消一律被拦下判失败。
 */
export type FixtureChains = "none" | "history" | "failed" | "running";

/** workflowId：项目的流水线（不给时按流水线五原流程）；SSS 电影 / 短剧时导演文档接口返回 sss 模式的文档。 */
export function createDramaFlowFixture(
    options: {
        style?: StyleFixture;
        tasks?: unknown[];
        videoModel?: string;
        imageModel?: string;
        flowEnabled?: boolean;
        chains?: FixtureChains;
        extraSegments?: number;
        workflowId?: string;
        name?: string;
        stopAtAssets?: boolean;
        directorSegments?: boolean;
        directorSetup?: "missing" | "legacy" | "current";
        handoffGate?: boolean;
        freezeRelay?: "ready" | "skipped";
        assetImages?: boolean;
    } = {},
) {
    const style = options.style ?? "none";
    const overrides = new Map<string, FixtureOverride>();
    const state = {
        segments: [
            { shotId: FIXTURE_SHOT_ID, unitId: FIXTURE_UNIT_ID, title: "S01 随行鲜", position: 0, durationMs: 15000, revisionId: "rev-1", spec: segmentSpec("S01", "阿婆把温橙汁推到林深手边。"), derived: false },
            // S02 是长内容段：镜头多，详情栏要滚动，用来验证最后的按钮也能滚到右下角浮层上方。
            { shotId: FIXTURE_SHOT_ID_2, unitId: FIXTURE_UNIT_ID, title: "S02 雨棚", position: 1, durationMs: 15000, revisionId: "rev-2", spec: longSegmentSpec("S02", "林深抬头看雨。", 10), derived: false },
        ],
        revision: 2,
        saves: 0,
        previews: 0,
        videoRoute: null as ProjectVideoRoute | null,
        /** 每次预览请求里真实发出的视频配置（含被拒的）。 */
        previewConfigs: [] as Array<Record<string, unknown>>,
        /** stopAtAssets 时「继续：角色场景资产」确认后真实发出的出图请求（只记不建任务）。 */
        imageQueues: [] as Array<{ assetIds?: string[]; imageTask?: { model?: string } }>,
    };
    // directorSegments：S01 是导演分镜单元（SSS / 本体论装出的段带 verbatim，提交用单元卡原文），S02 仍是原流程的段。
    if (options.directorSegments) Object.assign(state.segments[0].spec, { verbatim: "15秒，雨夜便民市场。@图1把温橙汁推到@图2手边。" });
    // extraSegments：再加几段短内容（S03…），出片列表变长、需要滚动，用来验证每张段卡的操作都点得到。
    for (let index = 0; index < (options.extraSegments ?? 0); index += 1) {
        const id = `S${String(index + 3).padStart(2, "0")}`;
        state.segments.push({ shotId: `shot-${id.toLowerCase()}`, unitId: FIXTURE_UNIT_ID, title: `${id} 续场`, position: index + 2, durationMs: 15000, revisionId: `rev-x${index}`, spec: segmentSpec(id, `${id}：雨停后林深推车离开。`), derived: false });
    }
    const calls: FixtureCall[] = [];
    const unknown: FixtureCall[] = [];
    const forbidden: FixtureCall[] = [];
    const handoffRequests: boolean[] = [];

    const project = {
        id: FIXTURE_PROJECT_ID,
        name: options.name ?? "新工作流无模型回归",
        description: "",
        status: "active",
        sourceType: "blank",
        visualType: "cg_real",
        aspectRatio: "9:16",
        runMode: "every_step",
        generationModel: "seedance-2.5",
        defaultVideoModel: options.videoModel ?? FIXTURE_VIDEO_MODEL,
        defaultImageModel: options.imageModel ?? "",
        bibleJson: JSON.stringify(bibleFor(style)),
        styleProfileJson: "",
        stylePresetId: "",
        ...(options.workflowId ? { workflowId: options.workflowId, workflowVersion: "1" } : {}),
        createdAt: now,
        updatedAt: now,
    };
    const unit = { id: FIXTURE_UNIT_ID, projectId: FIXTURE_PROJECT_ID, kind: "episode", title: "E01 随行鲜", sourceText: "", wordCount: 12, status: "draft", position: 0, createdAt: now, updatedAt: now };
    const fullUnit = { ...unit, sourceText: "<p>第一场：雨夜，阿婆把温橙汁递给林深。</p>" };
    const asset = (id: string, title: string, status: string) => ({
        id,
        projectId: FIXTURE_PROJECT_ID,
        title,
        category: "character",
        status: "active",
        usages: [],
        image: { status, ...(status === "failed" ? { error: "上游超时" } : {}) },
        character: { versionId: `${id}-v1`, version: 1, definition: { name: title, appearance: `${title}的外观` }, representations: [], visualStatus: "missing", voiceStatus: "missing" },
        createdAt: now,
        updatedAt: now,
    });
    // stopAtAssets：两个资产都还没出图（没有失败待办，主按钮才是「继续：角色场景资产」）。
    const assets: Array<Record<string, unknown>> = [asset(FIXTURE_ASSET_ID, "林深", "none"), asset(FIXTURE_ASSET_ID_2, "阿婆", options.stopAtAssets ? "none" : "failed")];
    // assetImages：林深主卡（竖版 90×160）带一张已出图的子卡（雨衣），另加一个方形图的场景，图片走资源文件接口（夹具回真 PNG）。
    if (options.assetImages) {
        const linshen = assets[0] as { image: unknown; character: Record<string, unknown> };
        linshen.image = { status: "bound" };
        linshen.character.representations = [{ id: "rep-linshen", role: "primary", resourceId: FIXTURE_IMAGE_PORTRAIT }];
        Object.assign(linshen, {
            variants: [
                {
                    id: "asset-linshen-rain",
                    projectId: FIXTURE_PROJECT_ID,
                    title: "林深·雨衣",
                    category: "character",
                    status: "active",
                    usages: [],
                    image: { status: "bound" },
                    // 和后端 ProjectAssetVariantSummary 同一形状：scenes 没指定时是空数组（后端把 nil 归成 []），look / promptEn 是字符串。
                    variant: { parentId: FIXTURE_ASSET_ID, parentTitle: "林深", label: "雨衣", kind: "costume", scenes: [], look: "深色雨衣，帽檐压低", promptEn: "" },
                    character: {
                        versionId: "asset-linshen-rain-v1",
                        version: 1,
                        definition: { name: "林深·雨衣" },
                        representations: [{ id: "rep-linshen-rain", role: "primary", resourceId: FIXTURE_IMAGE_PORTRAIT }],
                        visualStatus: "ready",
                        voiceStatus: "missing",
                    },
                    createdAt: now,
                    updatedAt: now,
                },
            ],
        });
        assets.push({
            id: "asset-market",
            projectId: FIXTURE_PROJECT_ID,
            title: "便民市场雨棚",
            category: "environment",
            status: "active",
            usages: [],
            mediaType: "image",
            storageKey: `resource:${FIXTURE_IMAGE_SQUARE}`,
            image: { status: "bound" },
            visual: { versionId: "asset-market-v1", version: 1, definition: { description: "雨夜便民市场的雨棚" } },
            createdAt: now,
            updatedAt: now,
        });
    }
    const stages = ["script", "style", "palette", "assets", "shots", "pack", "video", "cut"].map((key) => ({ key, label: key, state: key === "script" ? "done" : "pending" }));
    const segmentProgress = () =>
        state.segments.map((segment) => ({
            shotId: segment.shotId,
            unitId: segment.unitId,
            segmentId: segment.spec.id,
            title: segment.title,
            durationMs: segment.durationMs,
            overLimit: false,
            hasDialogue: true,
            script: true,
            refs: false,
            generated: false,
            reviewed: false,
            inCut: false,
            accepted: false,
            takes: 0,
        }));
    const progress = () => ({
        model: { id: "seedance-2.5", label: "Seedance 2.5", duration: { min: 4, max: 15 } },
        runMode: "every_step",
        previzGate: false,
        previzGateSetting: "off",
        segments: segmentProgress(),
        counts: { segments: state.segments.length, script: state.segments.length, refs: 0, generated: 0, takes: 0, reviewed: 0, inCut: 0, accepted: 0, overLimit: 0, issues: 0 },
        line: "",
        endStates: [],
        pipeline: options.stopAtAssets
            ? { runMode: "every_step", runModeLabel: "每步都问我", stages, current: "assets", stop: { stage: "assets", label: "角色场景资产", reason: "资产已提取，等你确认出图", action: "继续：角色场景资产", continuable: true } }
            : { runMode: "every_step", runModeLabel: "每步都问我", stages, current: "style" },
    });
    const workspace = () => ({ unit: fullUnit, assets, tasks: [], workflows: [], shots: [], shotRevisions: [], shotArtifacts: [], shotReferences: [], assetCandidates: [] });
    const catalog = {
        source: "system",
        channels: [
            {
                id: "CHANNEL_000001",
                displayName: "柚子系统渠道",
                sortOrder: 0,
                models: [
                    ...(options.handoffGate ? [{ modelKey: "fixture-text", displayName: "验收文本模型", available: true, capability: "text", priceTiers: [] }] : []),
                    { modelKey: "lec-vp-seedance-2-5-f", displayName: FIXTURE_VIDEO_MODEL_NAME, available: true, capability: "video", priceTiers: [], capabilityConfig: FIXTURE_VIDEO_CAPABILITY },
                    // stopAtAssets 时目录里有 imageModel 这个图片模型（确认面板的模型选择用它）；其余用例的 imageModel 可能故意是已下架的模型，不放进目录。
                    ...(options.stopAtAssets && options.imageModel ? [{ modelKey: options.imageModel.replace(/^CHANNEL_000001::/, ""), displayName: "验收图片模型", available: true, capability: "image", priceTiers: [] }] : []),
                ],
            },
        ],
    };

    const chainStep = (shotId: string, title: string, status: string, extra: Record<string, unknown> = {}) => ({
        shotId,
        revisionId: "rev-1",
        title,
        scene: "E01S01",
        contentSeconds: 15,
        generationSeconds: 15,
        model: FIXTURE_VIDEO_MODEL,
        status,
        phase: status === "running" ? "generating" : "",
        referenceCount: 0,
        ...extra,
    });
    const chainOf = (id: string, status: string, steps: unknown[], error?: string) => ({
        id,
        projectId: FIXTURE_PROJECT_ID,
        unitId: FIXTURE_UNIT_ID,
        status,
        stage: "",
        previewHash: "",
        totalMicrocredits: 0,
        whiteModelStoryboardEnabled: false,
        steps,
        ...(error ? { error } : {}),
    });
    const chainList = () => {
        if (options.freezeRelay)
            return [
                chainOf("freeze-history", "succeeded", [
                    chainStep(
                        FIXTURE_SHOT_ID_2,
                        "S02",
                        "succeeded",
                        options.freezeRelay === "ready"
                            ? { sourceShotId: FIXTURE_SHOT_ID, frameResourceId: "freeze-image", frameSelection: "freeze-mid" }
                            : { sourceShotId: FIXTURE_SHOT_ID, referenceSkipped: true, referenceWarning: "未使用上一段站位参考：截帧失败" },
                    ),
                ]),
            ];
        const mode = options.chains ?? "none";
        if (mode === "history") return [chainOf("chain-history", "succeeded", [chainStep(FIXTURE_SHOT_ID, "S01", "succeeded")])];
        if (mode === "failed") return [chainOf("chain-failed", "failed", [chainStep(FIXTURE_SHOT_ID, "S01", "failed", { error: "上游超时" })], "上游超时")];
        if (mode === "running") return [chainOf("chain-running", "running", [chainStep(FIXTURE_SHOT_ID, "S01", "running")])];
        return [];
    };
    const ok = (data: unknown): FixtureResponse => ({ status: 200, body: { code: 0, data, msg: "ok" } });
    const reject = (status: number, reason: string): FixtureResponse => ({ status, body: { code: status, msg: reason, reason } });
    /** 和后端 modelCapabilityMismatchError 投影出来的一样：reason、通用文案和 details（参数名、原因、逐条问题）。 */
    const capabilityReject = (issues: Array<{ name: string; reason: string }>, withDetails = true): FixtureResponse => ({
        status: 400,
        body: {
            code: 400,
            msg: "所选模型不支持当前请求",
            reason: "model_capability_not_supported",
            ...(withDetails && issues.length ? { details: { field: issues[0].name, issue: "unsupported_value", message: issues[0].reason, capabilityIssues: issues.map((issue) => ({ kind: "option", ...issue })) } } : {}),
        },
    });
    const base = `/projects/${FIXTURE_PROJECT_ID}`;

    const registry = {
        version: "fixture",
        default: "seedance-2.5",
        models: [
            {
                id: "seedance-2.5",
                label: "Seedance 2.5",
                vendor: "bytedance",
                kind: "video",
                match: ["seedance"],
                duration: { min: 4, max: 15, step: 1 },
                refs: { images: 9, videos: 3, audios: 3, total: 12 },
                frames_exclusive_with_refs: false,
                prompt_max_chars: 2000,
                resolutions: ["720p"],
                calibrated: true,
            },
        ],
    };

    // 应用外壳：一个普通（非管理员）验收账号；新工作流开关可关，用来验入口隐藏和直达拦截。
    const user = { id: "qa-user", username: "flow-qa", displayName: "无模型验收", role: "user", createdAt: now, updatedAt: now };
    const features = {
        welcomeEnabled: false,
        shortDramaEnabled: true,
        dramaFlowEnabled: options.flowEnabled ?? true,
        taskCenterEnabled: true,
        creditsEnabled: true,
        customChannelsEnabled: false,
        frontendModelsEnabled: true,
        pluginCenterEnabled: false,
        systemPluginsVisibleToUsers: false,
        inspirationSourcesVisible: false,
        imageWorkbenchEnabled: false,
        videoWorkbenchEnabled: false,
        skillContentVisible: false,
        configured: true,
    };

    /** 返回 undefined 表示这次请求一直不回（测「在读」）。 */
    function handle(method: string, rawPath: string, body?: unknown): FixtureResponse | undefined {
        const url = new URL(rawPath, "http://fixture.local");
        const path = url.pathname.replace(/^\/api(?=\/)/, "");
        const call = { method: method.toUpperCase(), path: `${path}${url.search}` };
        calls.push(call);
        const override = overrides.get(path);
        if (options.handoffGate && method.toUpperCase() === "POST" && path === `${base}/units/${FIXTURE_UNIT_ID}/drama/steps/chapter.shots/run`) {
            const independent = (body as { allowMissingHandoff?: boolean })?.allowMissingHandoff === true;
            handoffRequests.push(independent);
            if (independent) return reject(409, "fixture_independent_request_recorded");
            return { status: 409, body: { code: 409, msg: "第一集还没有交接卡", reason: "previous_handoff_required", details: { previousUnitId: "previous-episode", previousTitle: "第一集" } } };
        }
        if (override === "pending") return undefined;
        if (override === "error") return reject(500, "fixture_read_failed");
        if (override === "capability") return capabilityReject([{ name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" }]);
        if (override === "capability-bare") return capabilityReject([{ name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" }], false);
        if (call.method === "GET") {
            if (options.freezeRelay === "ready" && path === "/resources/freeze-image/file") return { status: 200, body: null, binary: FIXTURE_PNG_PORTRAIT, contentType: "image/png" };
            // 资源文件：assetImages 时回真 PNG（竖版 90×160 / 方形 120×120），预览器按原图比例显示。
            if (options.assetImages && path === `/resources/${FIXTURE_IMAGE_PORTRAIT}/file`) return { status: 200, body: null, binary: FIXTURE_PNG_PORTRAIT, contentType: "image/png" };
            if (options.assetImages && path === `/resources/${FIXTURE_IMAGE_SQUARE}/file`) return { status: 200, body: null, binary: FIXTURE_PNG_SQUARE, contentType: "image/png" };
            if (path === "/auth/session") return ok({ user, features });
            if (path === "/features") return ok({ features });
            if (path === "/drama/models") return ok({ models: registry });
            if (path === `${base}/core`) return ok({ project });
            if (path === `${base}/units`)
                return ok({
                    units: options.handoffGate
                        ? [
                              { ...unit, id: "previous-episode", title: "第一集", position: 0 },
                              { ...unit, title: "第二集", position: 1 },
                          ]
                        : [unit],
                    canvasCounts: {},
                });
            if (path === `${base}/assets`) return ok({ assets });
            if (path === `${base}/drama/progress`) return ok({ progress: progress() });
            if (path === `${base}/drama/bible`) return ok({ document: bibleFor(style) });
            if (path === `${base}/drama/brief`) return ok({ document: {} });
            if (path === `${base}/drama/video-routing`) return ok({ route: state.videoRoute });
            if (path === `${base}/drama/ontology`) {
                const doc = (kind: string, content: string, unitId = "") => ({ id: `doc-${kind}`, projectId: FIXTURE_PROJECT_ID, unitId, kind, version: 1, content, createdAt: now });
                return ok({
                    mode: options.workflowId === "pipeline-3" ? "ontology" : "sss",
                    bible: options.directorSetup === "missing" ? undefined : doc("bible", "## 世界\n> 林深：二十八岁男性。"),
                    bibleBasis: { episodes: options.directorSetup === "missing" ? 0 : 1, total: 1, legacy: options.directorSetup === "legacy", outdated: false },
                    designs: options.directorSetup === "current" ? [doc("design", "## 第一部分　本集设计\n雨夜相遇。\n## 第二部分　台词定稿表\n| 定稿台词 |\n| --- |\n| 收着。 |", FIXTURE_UNIT_ID)] : [],
                    concept: doc("concept", "## 立意页\n一句话：雨夜送橙汁。"),
                    world: doc("world", "## 世界基准段\n真人实拍质感的竖屏短剧。"),
                    episodes: [
                        doc(
                            "episode",
                            "## 第一部分　本集设计\n雨棚重逢。\n## 第二部分　台词定稿表\n收着。\n## 第三部分　讲戏本\n阿婆递杯。\n## 第四部分　单元总表\n雨棚一段。\n## 第五部分　单元卡\n\n### E1-U1｜短档｜叙事 14 秒＋定格 1 秒＝15 秒｜2 镜＋定格｜雨棚（本场 1 段）｜钩子\n\n## 第六部分　后期备忘\n雨棚。\n## 第七部分　集交接卡\n保持站位。\n## 第八部分　复看记录与待确认\n\n### 待确认\n1. 阿婆的口音。",
                            FIXTURE_UNIT_ID,
                        ),
                    ],
                    handoffs: [doc("handoff", "### 第 1 集交接卡\n林深在雨棚下。", FIXTURE_UNIT_ID)],
                });
            }
            if (path === `${base}/drama/episodes`)
                return ok({ episodes: [{ unitId: FIXTURE_UNIT_ID, title: unit.title, position: unit.position, wordCount: unit.wordCount, sources: [], shots: state.segments.length, hasShotlist: true }], adaptedChapterIds: [] });
            if (path === `${base}/units/${FIXTURE_UNIT_ID}/workspace`) return ok(workspace());
            if (path === `${base}/drama/segments`) return ok({ segments: state.segments });
            if (path === `${base}/units/${FIXTURE_UNIT_ID}/drama/shotlist`) return ok({ shotlist: null });
            if (path === `${base}/units/${FIXTURE_UNIT_ID}/drama/scenes`) return ok({ unitId: FIXTURE_UNIT_ID, title: unit.title, episode: 1, fromChapters: false, rule: 15, scenes: [], paletteReady: false });
            if (path === `${base}/video-chains`) return ok({ chains: chainList() });
            if (path === "/drama/shot-vocab") return ok({ vocab: { sizes: ["远景", "全景", "中景", "近景", "特写"], cameras: ["固定机位", "推", "拉", "摇"], transitions: [] } });
            if (path === "/model-catalog") return ok(catalog);
            // 短剧可用模型目录（服务端无人值守能用的平台模型）：按同一份系统目录列出，步骤模型卡据此判断能不能选。
            if (path === "/dramas/models") {
                const options: Record<string, unknown[]> = { text: [], image: [], video: [] };
                for (const channel of catalog.channels) {
                    for (const model of channel.models) {
                        const video = "capabilityConfig" in model ? model.capabilityConfig?.video : undefined;
                        options[model.capability]?.push({
                            choice: { channelId: channel.id, modelKey: model.modelKey },
                            capability: model.capability,
                            label: model.displayName,
                            priceLabel: "",
                            videoRatios: video?.ratios,
                        });
                    }
                }
                return ok({ defaults: {}, options, unavailable: {} });
            }
            if (path === "/wallet") return ok({ account: { availableMicrocredits: 9_000_000_000, frozenMicrocredits: 0 }, transactions: [], total: 0, page: 1, pageSize: 1 });
            if (path === "/tasks") return ok(options.tasks ?? []);
        }
        if (call.method === "PUT" && path === `${base}/drama/segments`) {
            const input = body as { segments?: Array<{ id: string; dur: number }> };
            for (const spec of input?.segments || []) {
                const target = state.segments.find((segment) => segment.spec.id === spec.id);
                if (!target) return reject(400, "segment_not_found");
                state.revision += 1;
                Object.assign(target, { spec, durationMs: Math.round(spec.dur * 1000), revisionId: `rev-${state.revision}` });
            }
            state.saves += 1;
            return ok({ segments: state.segments });
        }
        if (call.method === "POST" && path === `${base}/drama/video-routing/override`) {
            const input = body as ProjectVideoOverride;
            if (!input.model || input.revision !== (state.videoRoute?.revision || 0)) return reject(409, "project_video_route_changed");
            state.videoRoute = { status: "manual", fromModel: input.fromModel, model: input.model, reason: "用户手选", successRate: null, sampledAt: null, changedAt: now, revision: input.revision + 1 };
            return ok({ route: state.videoRoute });
        }
        // 出片花费预览只读：返回整链预估，不建任务、不扣费。真正提交（POST /video-chains）、重试、取消都落到下面的拦截。
        if (call.method === "POST" && path === `${base}/video-chains/preview`) {
            // 按权威能力校验真实发出的配置（记下来供测试断言）：同步音频必须是 false，时长必须是 15 或 30 秒档。
            const input = body as { shotId?: string; model?: { model?: string; input?: { config?: Record<string, unknown> } } };
            const config = input?.model?.input?.config || {};
            state.previewConfigs.push({ ...config });
            const issues = [
                ...(String(config.videoGenerateAudio) === "true" ? [{ name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" }] : []),
                ...(!["15", "30"].includes(String(config.videoSeconds)) ? [{ name: "videoSeconds", reason: "参数 时长超出支持范围" }] : []),
            ];
            if (issues.length) return capabilityReject(issues);
            state.previews += 1;
            const steps = state.segments
                .filter((segment) => segment.shotId === input?.shotId || !input?.shotId)
                .map((segment) => chainStep(segment.shotId, segment.spec.id, "waiting", { phase: "waiting", model: input.model?.model || FIXTURE_VIDEO_MODEL, quote: { amountMicrocredits: 1_500_000 } }));
            return ok({ chain: { ...chainOf("", "preview", steps), previewHash: `preview-${state.previews}`, totalMicrocredits: 1_500_000 * steps.length } });
        }
        // 编译提示词只读不扣费，按当前段给一个确定的结果。
        if (call.method === "POST" && /^\/projects\/[^/]+\/shots\/[^/]+\/compile$/.test(path)) {
            const shotId = path.split("/")[4];
            const segment = state.segments.find((item) => item.shotId === shotId);
            const prompt = segment ? segment.spec.shots.map((shot) => shot.text).join("\n") : "";
            return ok({ compiled: { shotId, spec: segment?.spec, derived: false, result: { segment: segment?.spec.id || "", model: FIXTURE_VIDEO_MODEL, duration: 15, prompt, files: [], chars: prompt.length, issues: [] } } });
        }
        // 资源显示授权（批量取图片显示地址，只读，不改任何数据）：assetImages 时按后端形状回平台代理地址，指向上面回真 PNG 的资源文件接口。
        if (options.assetImages && call.method === "POST" && path === "/resources/access") {
            const requests = (Array.isArray(body) ? body : []) as Array<{ resourceId?: string; variant?: string }>;
            const issuedAt = new Date().toISOString();
            const items = requests.map((item) => {
                const resourceId = String(item.resourceId || "");
                if (resourceId !== FIXTURE_IMAGE_PORTRAIT && resourceId !== FIXTURE_IMAGE_SQUARE) return { resourceId, error: { msg: "资源不存在" } };
                const variant = item.variant || "original";
                return {
                    resourceId,
                    access: { resourceId, requestedVariant: variant, actualVariant: variant, url: `/api/resources/${resourceId}/file`, delivery: "platform-proxy", issuedAt, refreshAt: new Date(Date.now() + 10 * 60_000).toISOString(), revision: "r1" },
                };
            });
            return ok({ items });
        }
        // 「继续：角色场景资产」确认面板提交的出图请求：只记下选了哪些资产、用的哪个模型，不建任务。
        if (options.stopAtAssets && call.method === "POST" && path === `${base}/drama/assets/images`) {
            const input = (body || {}) as { assetIds?: string[]; imageTask?: { model?: string } };
            state.imageQueues.push(input);
            return ok({ queued: input.assetIds || [], waiting: [], failed: [] });
        }
        if (call.method !== "GET") {
            forbidden.push(call);
            return reject(403, "fixture_write_blocked");
        }
        unknown.push(call);
        return reject(404, "fixture_not_found");
    }

    return { handle, calls, unknown, forbidden, state, project, overrides, catalog, handoffRequests };
}

export type DramaFlowFixture = ReturnType<typeof createDramaFlowFixture>;

/** 运行记录夹具：一次运行一条，billing 不给就是没有计费单。 */
export function fixtureTask(id: string, billing?: { amountMicrocredits: number; actualAmountMicrocredits?: number; status: string }, model = FIXTURE_VIDEO_MODEL) {
    return { id, projectId: FIXTURE_PROJECT_ID, type: "canvas_text", status: "succeeded", prompt: "", attempts: 1, model, createdAt: now, updatedAt: now, ...(billing ? { billing } : {}) };
}
