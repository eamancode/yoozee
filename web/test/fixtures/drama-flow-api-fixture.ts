/**
 * 短剧新工作流的无模型接口夹具：组件生命周期回归（happy-dom）和真实路由 Chrome E2E 共用。
 *
 * 只模拟读接口和分镜保存（PUT /drama/segments）。不模拟任何生成接口：生成、出图、出片、
 * 色卡、提取资产等写请求一律记进 forbidden 并返回 403，测试据此断言「没有提交生成」。
 * 没列出的读接口返回 404 并记进 unknown，不伪造数据。
 */

import { defaultModelCapabilityConfig } from "../../src/lib/model-capabilities";

export const FIXTURE_PROJECT_ID = "p-flow-qa";
export const FIXTURE_UNIT_ID = "u-flow-e1";
export const FIXTURE_SHOT_ID = "shot-s01";
export const FIXTURE_SHOT_ID_2 = "shot-s02";
export const FIXTURE_ASSET_ID = "asset-linshen";
export const FIXTURE_ASSET_ID_2 = "asset-apo";
export const FIXTURE_VIDEO_MODEL = "CHANNEL_000001::lec-vp-seedance-2-5-f";
export const FIXTURE_VIDEO_MODEL_NAME = "Seedance 2.5 Fast";

export type StyleFixture = "none" | "anchor_only" | "locked";

type Envelope = { code: number; data?: unknown; msg: string; reason?: string; details?: Record<string, unknown> };
export type FixtureResponse = { status: number; body: Envelope };
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

export function createDramaFlowFixture(options: { style?: StyleFixture; tasks?: unknown[]; videoModel?: string; imageModel?: string; flowEnabled?: boolean; chains?: FixtureChains; extraSegments?: number } = {}) {
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
        /** 每次预览请求里真实发出的视频配置（含被拒的）。 */
        previewConfigs: [] as Array<Record<string, unknown>>,
    };
    // extraSegments：再加几段短内容（S03…），出片列表变长、需要滚动，用来验证每张段卡的操作都点得到。
    for (let index = 0; index < (options.extraSegments ?? 0); index += 1) {
        const id = `S${String(index + 3).padStart(2, "0")}`;
        state.segments.push({ shotId: `shot-${id.toLowerCase()}`, unitId: FIXTURE_UNIT_ID, title: `${id} 续场`, position: index + 2, durationMs: 15000, revisionId: `rev-x${index}`, spec: segmentSpec(id, `${id}：雨停后林深推车离开。`), derived: false });
    }
    const calls: FixtureCall[] = [];
    const unknown: FixtureCall[] = [];
    const forbidden: FixtureCall[] = [];

    const project = {
        id: FIXTURE_PROJECT_ID,
        name: "新工作流无模型回归",
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
    const assets = [asset(FIXTURE_ASSET_ID, "林深", "none"), asset(FIXTURE_ASSET_ID_2, "阿婆", "failed")];
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
        pipeline: { runMode: "every_step", runModeLabel: "每步都问我", stages, current: "style" },
    });
    const workspace = () => ({ unit: fullUnit, assets, tasks: [], workflows: [], shots: [], shotRevisions: [], shotArtifacts: [], shotReferences: [], assetCandidates: [] });
    const catalog = {
        source: "system",
        channels: [
            {
                id: "CHANNEL_000001",
                displayName: "柚子系统渠道",
                sortOrder: 0,
                models: [{ modelKey: "lec-vp-seedance-2-5-f", displayName: FIXTURE_VIDEO_MODEL_NAME, available: true, capability: "video", priceTiers: [], capabilityConfig: FIXTURE_VIDEO_CAPABILITY }],
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
        if (override === "pending") return undefined;
        if (override === "error") return reject(500, "fixture_read_failed");
        if (override === "capability") return capabilityReject([{ name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" }]);
        if (override === "capability-bare") return capabilityReject([{ name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" }], false);
        if (call.method === "GET") {
            if (path === "/auth/session") return ok({ user, features });
            if (path === "/features") return ok({ features });
            if (path === "/drama/models") return ok({ models: registry });
            if (path === `${base}/core`) return ok({ project });
            if (path === `${base}/units`) return ok({ units: [unit], canvasCounts: {} });
            if (path === `${base}/assets`) return ok({ assets });
            if (path === `${base}/drama/progress`) return ok({ progress: progress() });
            if (path === `${base}/drama/bible`) return ok({ document: bibleFor(style) });
            if (path === `${base}/drama/brief`) return ok({ document: {} });
            if (path === `${base}/drama/episodes`) return ok({ episodes: [{ id: FIXTURE_UNIT_ID, title: unit.title }], adaptedChapterIds: [] });
            if (path === `${base}/units/${FIXTURE_UNIT_ID}/workspace`) return ok(workspace());
            if (path === `${base}/drama/segments`) return ok({ segments: state.segments });
            if (path === `${base}/units/${FIXTURE_UNIT_ID}/drama/shotlist`) return ok({ shotlist: null });
            if (path === `${base}/units/${FIXTURE_UNIT_ID}/drama/scenes`) return ok({ unitId: FIXTURE_UNIT_ID, title: unit.title, episode: 1, fromChapters: false, rule: 15, scenes: [], paletteReady: false });
            if (path === `${base}/video-chains`) return ok({ chains: chainList() });
            if (path === "/drama/shot-vocab") return ok({ vocab: { sizes: ["远景", "全景", "中景", "近景", "特写"], cameras: ["固定机位", "推", "拉", "摇"], transitions: [] } });
            if (path === "/model-catalog") return ok(catalog);
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
        // 出片花费预览只读：返回整链预估，不建任务、不扣费。真正提交（POST /video-chains）、重试、取消都落到下面的拦截。
        if (call.method === "POST" && path === `${base}/video-chains/preview`) {
            // 按权威能力校验真实发出的配置（记下来供测试断言）：同步音频必须是 false，时长必须是 15 或 30 秒档。
            const input = body as { shotId?: string; model?: { input?: { config?: Record<string, unknown> } } };
            const config = input?.model?.input?.config || {};
            state.previewConfigs.push({ ...config });
            const issues = [
                ...(String(config.videoGenerateAudio) === "true" ? [{ name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" }] : []),
                ...(!["15", "30"].includes(String(config.videoSeconds)) ? [{ name: "videoSeconds", reason: "参数 时长超出支持范围" }] : []),
            ];
            if (issues.length) return capabilityReject(issues);
            state.previews += 1;
            const steps = state.segments.filter((segment) => segment.shotId === input?.shotId || !input?.shotId).map((segment) => chainStep(segment.shotId, segment.spec.id, "waiting", { phase: "waiting", quote: { amountMicrocredits: 1_500_000 } }));
            return ok({ chain: { ...chainOf("", "preview", steps), previewHash: `preview-${state.previews}`, totalMicrocredits: 1_500_000 * steps.length } });
        }
        // 编译提示词只读不扣费，按当前段给一个确定的结果。
        if (call.method === "POST" && /^\/projects\/[^/]+\/shots\/[^/]+\/compile$/.test(path)) {
            const shotId = path.split("/")[4];
            const segment = state.segments.find((item) => item.shotId === shotId);
            const prompt = segment ? segment.spec.shots.map((shot) => shot.text).join("\n") : "";
            return ok({ compiled: { shotId, spec: segment?.spec, derived: false, result: { segment: segment?.spec.id || "", model: FIXTURE_VIDEO_MODEL, duration: 15, prompt, files: [], chars: prompt.length, issues: [] } } });
        }
        if (call.method !== "GET") {
            forbidden.push(call);
            return reject(403, "fixture_write_blocked");
        }
        unknown.push(call);
        return reject(404, "fixture_not_found");
    }

    return { handle, calls, unknown, forbidden, state, project, overrides, catalog };
}

export type DramaFlowFixture = ReturnType<typeof createDramaFlowFixture>;

/** 运行记录夹具：一次运行一条，billing 不给就是没有计费单。 */
export function fixtureTask(id: string, billing?: { amountMicrocredits: number; actualAmountMicrocredits?: number; status: string }, model = FIXTURE_VIDEO_MODEL) {
    return { id, projectId: FIXTURE_PROJECT_ID, type: "canvas_text", status: "succeeded", prompt: "", attempts: 1, model, createdAt: now, updatedAt: now, ...(billing ? { billing } : {}) };
}
