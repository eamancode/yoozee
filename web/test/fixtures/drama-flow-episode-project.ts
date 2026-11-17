import type { ProjectUnit } from "@/services/api/projects";
import { FIXTURE_PROJECT_ID, type DramaFlowFixture, type FixtureResponse } from "./drama-flow-api-fixture";

/** 同一份服务端夹具可跨 QueryClient 冷启动。列表只返回摘要，正文要按真实单元 ID 读工作区。 */
export function createEpisodeProject() {
    const base = `/projects/${FIXTURE_PROJECT_ID}`;
    const collection = `${base}/units`;
    const ok = (data: unknown): FixtureResponse => ({ status: 200, body: { code: 0, msg: "ok", data } });
    const fail = (msg: string): FixtureResponse => ({ status: 409, body: { code: 409, msg, reason: "fixture_write_failed" } });
    const makeUnit = (id: string, title: string, position: number, kind = "episode", sourceText = `<p>${title}的独立正文</p>`): ProjectUnit => ({
        id,
        projectId: FIXTURE_PROJECT_ID,
        kind,
        title,
        sourceText,
        position,
        wordCount: sourceText.replace(/<[^>]*>/g, "").length,
        status: "draft",
        createdAt: "2026-11-12T08:00:00Z",
        updatedAt: "2026-11-12T08:00:00Z",
    });
    const source = makeUnit("source-only", "原文章节", 0, "chapter");
    const units = [source, makeUnit("ep-a", "雨棚", 1), makeUnit("ep-b", "渡口", 2), makeUnit("ep-c", "归家", 3)];
    const episodes = () => units.filter((unit) => unit.kind === "episode");
    const writes: Array<{ method: string; path: string; body: unknown }> = [];
    let segments: DramaFlowFixture["state"]["segments"] | undefined;
    let sequence = 3;
    const fixture = {
        units,
        writes,
        rejectNext: "",
        attach(api: DramaFlowFixture) {
            const template = api.state.segments[0];
            // 旧工作台的合法数据：原文章节也可以有镜头，项目进度会返回它。
            segments ??= units.map((unit) => ({
                ...template,
                unitId: unit.id,
                shotId: `shot-${unit.id}`,
                title: `${unit.title}长镜头`,
                revisionId: `stable-revision-${unit.id}`,
                spec: { ...template.spec, title: `${unit.title}长镜头` },
            }));
            api.state.segments = segments;
            const fallback = api.handle;
            api.handle = (method, rawPath, body) => {
                const url = new URL(rawPath, "http://fixture.local");
                const path = url.pathname;
                const verb = method.toUpperCase();
                if (verb !== "GET") writes.push({ method: verb, path, body });
                if (verb === "GET" && path === collection) return ok({ units: units.map((unit) => ({ ...unit, sourceText: "" })), canvasCounts: { "ep-b": 1 } });
                if (verb === "GET" && path === `${base}/drama/episodes`) {
                    return ok({
                        episodes: episodes().map((unit) => ({
                            unitId: unit.id,
                            title: unit.title,
                            position: unit.position,
                            wordCount: unit.wordCount,
                            sources: [],
                            shots: api.state.segments.filter((shot) => shot.unitId === unit.id).length,
                            hasShotlist: api.state.segments.some((shot) => shot.unitId === unit.id),
                        })),
                        adaptedChapterIds: [],
                    });
                }
                if (verb === "GET" && path === `${base}/drama/ontology`) {
                    return ok({
                        mode: "sss",
                        episodes: [],
                        designs: [],
                        handoffs: episodes().map((unit) => ({
                            id: `handoff-${unit.id}`,
                            projectId: FIXTURE_PROJECT_ID,
                            unitId: unit.id,
                            kind: "handoff",
                            version: 1,
                            content: `交接：${unit.title}中留下的纸条`,
                            createdAt: unit.createdAt,
                        })),
                    });
                }
                if (verb === "GET" && path === `${base}/drama/segments`) return ok({ segments: api.state.segments.filter((shot) => shot.unitId === url.searchParams.get("unitId")) });
                if (fixture.rejectNext === verb && (path === collection || path.startsWith(`${collection}/`))) {
                    fixture.rejectNext = "";
                    return fail("测试请求失败，请重试");
                }
                if (verb === "POST" && path === collection) {
                    const input = body as { kind: string; title: string; sourceText?: string };
                    if (input.kind !== "episode" || !input.title.trim()) return fail("剧集标题必填");
                    const unit = makeUnit(`new-ep-${++sequence}`, input.title, Math.max(0, ...units.map((u) => u.position)) + 1, input.kind, input.sourceText || "");
                    units.push(unit);
                    return ok({ unit });
                }
                if (path.startsWith(`${collection}/`)) {
                    const [id, ...rest] = path.slice(collection.length + 1).split("/");
                    const resource = rest.join("/");
                    const unit = units.find((candidate) => candidate.id === id);
                    if (!unit) return fail("剧集不存在");
                    if (verb === "GET" && resource === "workspace") return ok({ unit, assets: [], tasks: [], workflows: [], shots: [], shotRevisions: [], shotArtifacts: [], shotReferences: [], assetCandidates: [] });
                    if (verb === "GET" && resource === "drama/scenes") return ok({ unitId: id, title: unit.title, episode: episodes().indexOf(unit) + 1, fromChapters: false, rule: 15, scenes: [], paletteReady: false });
                    if (verb === "GET" && resource === "drama/shotlist") return ok({ shotlist: null });
                    if (verb === "PATCH" && !resource && unit.kind === "episode") {
                        unit.sourceText = (body as { sourceText: string }).sourceText;
                        return ok({ unit });
                    }
                    if (verb === "DELETE" && !resource && unit.kind === "episode") {
                        units.splice(units.indexOf(unit), 1);
                        segments = api.state.segments = api.state.segments.filter((shot) => shot.unitId !== id);
                        return ok({ id });
                    }
                }
                return fallback(method, rawPath, body);
            };
        },
    };
    return fixture;
}
