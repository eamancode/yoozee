import { afterAll, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

// 只把传输层换成 stub，drama.ts 本身是生产代码（仓库既有做法：临时模块，不用 mock.module）。
const dir = mkdtempSync(join(tmpdir(), "drama-api-"));
const requestPath = join(dir, "request.ts");
writeFileSync(
    requestPath,
    [
        "export class ApiError extends Error {",
        "    constructor(message, options = {}) { super(message); this.name = 'ApiError'; this.status = options.status; this.code = options.code; this.reason = options.reason; this.details = options.details; this.cause = options.cause; }",
        "}",
        "export const calls = [];",
        "export const responses = {};",
        "export const failures = {};",
        "const record = (method, url, extra = {}) => { calls.push({ method, url, ...extra }); if (failures[url]) throw failures[url]; return responses[url]; };",
        "export const http = {",
        "    get: async (url, config) => record('get', url, { config }),",
        "    post: async (url, body) => record('post', url, { body }),",
        "    put: async (url, body) => record('put', url, { body }),",
        "    patch: async (url, body) => record('patch', url, { body }),",
        "    raw: async (config) => record('raw', config.url, { config }),",
        "};",
    ].join("\n"),
);
const source = readFileSync(new URL("../src/services/api/drama.ts", import.meta.url), "utf8");
const dramaPath = join(dir, "drama.ts");
const errorCodesPath = fileURLToPath(new URL("../src/services/api/error-codes.ts", import.meta.url));
writeFileSync(dramaPath, source.replace('"@/services/api/request"', JSON.stringify(requestPath)).replace('"@/services/api/error-codes"', JSON.stringify(errorCodesPath)));
const drama: typeof import("../src/services/api/drama") = await import(dramaPath);
const transport = await import(requestPath);

const lastCall = () => transport.calls[transport.calls.length - 1];

// downloadDramaDelivery 走浏览器 API，这里只补最小 DOM 桩。
const nativeCreateObjectURL = URL.createObjectURL;
const nativeRevokeObjectURL = URL.revokeObjectURL;
const nativeDocument = (globalThis as { document?: unknown }).document;
const nativeWindow = (globalThis as { window?: unknown }).window;
let clicked: Array<{ download: string; href: string }> = [];

// happy-dom（本次合并新增的 devDependency）会把 globalThis.window / document 定义成
// 只读属性，直接赋值会抛 TypeError: Attempted to assign to readonly property。
// 这里统一用 defineProperty 覆盖，并保持可配置，便于 afterAll 还原。
function setGlobal(name: "window" | "document", value: unknown) {
    Object.defineProperty(globalThis, name, { value, configurable: true, writable: true, enumerable: true });
}

beforeEach(() => {
    transport.calls.length = 0;
    for (const key of Object.keys(transport.responses)) delete transport.responses[key];
    for (const key of Object.keys(transport.failures)) delete transport.failures[key];
    clicked = [];
    URL.createObjectURL = (() => "blob:drama") as typeof URL.createObjectURL;
    URL.revokeObjectURL = (() => undefined) as typeof URL.revokeObjectURL;
    // 生产代码用 window.setTimeout 延迟释放 object URL；Bun 没有 window，这里补上。
    setGlobal("window", { setTimeout: (fn: () => void) => setTimeout(fn, 0) });
    setGlobal("document", {
        createElement: () => ({
            set href(value: string) {
                clicked.push({ href: value, download: "" });
            },
            set download(value: string) {
                if (clicked.length) clicked[clicked.length - 1]!.download = value;
            },
            click: () => undefined,
            remove: () => undefined,
        }),
        body: { appendChild: () => undefined },
    });
});

afterAll(() => {
    URL.createObjectURL = nativeCreateObjectURL;
    URL.revokeObjectURL = nativeRevokeObjectURL;
    setGlobal("document", nativeDocument);
    setGlobal("window", nativeWindow);
    rmSync(dir, { recursive: true, force: true });
});

describe("短剧接口请求合同", () => {
    test("模型目录、进度、证据、交付都打对应路径", async () => {
        await drama.listDramaModels();
        expect(lastCall()).toMatchObject({ method: "get", url: "/drama/models" });

        await drama.getDramaProgress("p1");
        expect(lastCall()).toMatchObject({ method: "get", url: "/projects/p1/drama/progress" });

        await drama.listDramaEvidence("p1");
        expect(lastCall()).toMatchObject({ method: "get", url: "/projects/p1/drama/evidence" });

        transport.responses["/projects/p1/drama/delivery"] = { data: new Blob(["zip"]), headers: {} };
        await drama.downloadDramaDelivery("p1");
        expect(lastCall()).toMatchObject({ method: "raw", url: "/projects/p1/drama/delivery" });
        expect(lastCall().config).toMatchObject({ method: "get", responseType: "blob" });
    });

    test("写接口把请求体原样交给后端（需求文档、编译、证据、选定、门禁）", async () => {
        await drama.putDramaBrief("p1", { title: "夜班" });
        expect(lastCall()).toMatchObject({ method: "put", url: "/projects/p1/drama/brief", body: { document: { title: "夜班" } } });

        await drama.compileProjectShot("p1", "shot-1", { model: "seedance-2.5" });
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/shots/shot-1/compile", body: { model: "seedance-2.5" } });

        await drama.compileProjectShot("p1", "shot-1");
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/shots/shot-1/compile", body: {} });

        await drama.createDramaEvidence("p1", { artifactId: "a1", kind: "faces", passed: true, note: "同一个人" });
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/drama/evidence", body: { artifactId: "a1", kind: "faces", passed: true, note: "同一个人" } });

        await drama.pickShotArtifact("p1", "a1", "眼神对");
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/artifacts/a1/pick", body: { reason: "眼神对" } });

        await drama.framesShotArtifact("p1", "a1");
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/artifacts/a1/frames", body: {} });

        await drama.transcribeShotArtifact("p1", "a1");
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/artifacts/a1/transcribe", body: {} });

        await drama.runDramaGate("p1", { targetLufs: -16 });
        expect(lastCall()).toMatchObject({ method: "post", url: "/projects/p1/drama/gate", body: { targetLufs: -16 } });
    });
});

describe("镜头表接口", () => {
    test("读写都打章节下的 drama/shotlist，写入把分镜行、段长规则和现有镜头 ID 原样交给后端", async () => {
        await drama.getDramaShotlist("p1", "u 1");
        expect(lastCall()).toMatchObject({ method: "get", url: "/projects/p1/units/u%201/drama/shotlist" });

        const rows = [{ shotNumber: 1, spec: { id: "E01S01-01" } }];
        await drama.putDramaShotlist("p1", "u1", { rows, rule: { maxSeconds: 15, minSeconds: 4 }, expectedShotIds: ["s1"] });
        expect(lastCall()).toMatchObject({ method: "put", url: "/projects/p1/units/u1/drama/shotlist", body: { rows, rule: { maxSeconds: 15, minSeconds: 4 }, expectedShotIds: ["s1"] } });
    });

    test("检查不过时从错误里取出规则项；别的错误不当成检查结果", () => {
        const check = { kind: "shots", errors: 1, warnings: 0, items: [{ level: "error", rule: "R-SHOT-025", message: "情绪弧不在词库里", shotId: "E01S01-01" }], skipped: [] };
        const failed = new transport.ApiError("镜头表检查有 1 个 error", { status: 400, reason: "shotlist_check_failed", details: { check } });
        expect(drama.shotlistCheckFailure(failed)).toEqual(check);
        expect(drama.shotlistCheckFailure(new transport.ApiError("x", { status: 400, reason: "segment_too_long", details: { check } }))).toBeUndefined();
        expect(drama.shotlistCheckFailure(new transport.ApiError("x", { status: 400, reason: "shotlist_check_failed" }))).toBeUndefined();
        expect(drama.shotlistCheckFailure(new Error("network"))).toBeUndefined();
    });
});

describe("交付包下载", () => {
    test("优先用 content-disposition 里的 UTF-8 文件名，并触发一次下载", async () => {
        transport.responses["/projects/p1/drama/delivery"] = {
            data: new Blob(["zip"]),
            headers: { "content-disposition": "attachment; filename*=UTF-8''%E5%A4%9C%E7%8F%AD-%E4%BA%A4%E4%BB%98%E5%8C%85.zip" },
        };
        const fileName = await drama.downloadDramaDelivery("p1", "回退名.zip");
        expect(fileName).toBe("夜班-交付包.zip");
        expect(clicked).toEqual([{ href: "blob:drama", download: "夜班-交付包.zip" }]);
    });

    test("没有响应头时用调用方给的兜底名", async () => {
        transport.responses["/projects/p1/drama/delivery"] = { data: new Blob(["zip"]), headers: {} };
        expect(await drama.downloadDramaDelivery("p1", "夜班-交付包.zip")).toBe("夜班-交付包.zip");
        expect(clicked[0]?.download).toBe("夜班-交付包.zip");
    });

    test("失败响应是 Blob 包着的 JSON 时，还原成可读错误", async () => {
        const blob = new Blob([JSON.stringify({ msg: "还没有导出的成片", code: 40900, reason: "drama_gate_pending" })], { type: "application/json" });
        transport.failures["/projects/p1/drama/delivery"] = new transport.ApiError("Request failed with status code 409", {
            status: 409,
            cause: { response: { data: blob } },
        });
        const thrown = await drama.downloadDramaDelivery("p1").catch((error) => error);
        expect(thrown).toBeInstanceOf(transport.ApiError);
        expect(thrown.message).toBe("还没有导出的成片");
        expect(thrown).toMatchObject({ status: 409, code: 40900, reason: "drama_gate_pending" });
    });

    test("失败响应不是 JSON 时保留原始错误，不吞掉", async () => {
        const original = new transport.ApiError("请求已被服务端拒绝", { status: 403 });
        transport.failures["/projects/p1/drama/delivery"] = original;
        expect(await drama.downloadDramaDelivery("p1").catch((error) => error)).toBe(original);
    });
});

describe("证据指标解析", () => {
    const evidence = (metricsJson: string) => ({ id: "e", projectId: "p", kind: "gate", passed: true, metricsJson, note: "", createdAt: "" }) as never;

    test("解析对象；null 与标量退回空对象", () => {
        expect(drama.parseEvidenceMetrics(evidence('{"cer":0.125,"checks":[{"name":"响度","ok":true}]}'))).toEqual({ cer: 0.125, checks: [{ name: "响度", ok: true }] });
        expect(drama.parseEvidenceMetrics(evidence(""))).toEqual({});
        expect(drama.parseEvidenceMetrics(evidence("{不是 JSON"))).toEqual({});
        expect(drama.parseEvidenceMetrics(evidence("null"))).toEqual({});
        expect(drama.parseEvidenceMetrics(evidence("42"))).toEqual({});
        // 数组也是 object，会原样返回；调用方只按具名字段取值，取不到就是 undefined。
        expect(drama.parseEvidenceMetrics(evidence("[1,2]"))).toEqual([1, 2] as never);
        expect((drama.parseEvidenceMetrics(evidence("[1,2]")) as { cer?: number }).cer).toBeUndefined();
    });
});

describe("短剧流程步骤（M6）", () => {
    test("生成分段只发「对这一章跑这一步」：路径带步骤 ID，body 只有文本模型选择", async () => {
        transport.responses["/projects/p%201/units/u1/drama/steps/chapter.shots/run"] = { task: { id: "t1" }, usage: { step: "chapter.shots" } };
        const result = await drama.runDramaStep("p 1", "u1", "chapter.shots", { model: "deepseek", config: { model: "deepseek" } });
        expect(result.task.id).toBe("t1");
        expect(lastCall()).toEqual({ method: "post", url: "/projects/p%201/units/u1/drama/steps/chapter.shots/run", body: { model: "deepseek", config: { model: "deepseek" } } });
        await drama.getDramaStepUsage("p1", "chapter.shots");
        expect(lastCall()).toMatchObject({ method: "get", url: "/projects/p1/drama/steps/chapter.shots" });
    });

    test("风格档案按后端画风对照表推荐，与 StyleMap.ProfileFor 同一规则", () => {
        const styleMap = JSON.parse(readFileSync(new URL("../../backend/internal/drama/style_map.json", import.meta.url), "utf8"));
        expect(drama.recommendDramaStyleProfile(styleMap, { selection: { world: "suspense", medium: "live-action" }, title: "悬疑犯罪 · 暗黑诡谲 · 真人实拍", tags: [] })).toBe("live-realism");
        expect(drama.recommendDramaStyleProfile(styleMap, { selection: { world: "xianxia", medium: "3d-anime" }, title: "仙侠", tags: [] })).toBe("anime-3d");
        expect(drama.recommendDramaStyleProfile(styleMap, { selection: { medium: "ink" } })).toBe("anime-3d");
        expect(drama.recommendDramaStyleProfile(styleMap, { title: "我的国漫风", tags: [] })).toBe("anime-3d");
        expect(drama.recommendDramaStyleProfile(styleMap, { title: "自定义", tags: ["写实"] })).toBe("live-realism");
        expect(drama.recommendDramaStyleProfile(styleMap, undefined)).toBe(styleMap.default);
    });
});
