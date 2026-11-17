import type { DramaProgress } from "@/services/api/project-drama";
import type { VideoChain, VideoChainRequest } from "@/services/api/project-video-chains";
import type { ProjectVideoRoute } from "@/services/api/project-video-routing";
import { createEpisodeProject } from "./drama-flow-episode-project";
import { FIXTURE_PROJECT_ID, FIXTURE_VIDEO_MODEL, type DramaFlowFixture, type FixtureResponse } from "./drama-flow-api-fixture";

const projectId = FIXTURE_PROJECT_ID;
const unitId = "ep-a";
const shotId = "shot-ep-a";
const chainId = "journey-chain";
const automaticKey = "lec-journey-seedance-2-5";
const automaticModel = `CHANNEL_000002::${automaticKey}`;
const automaticName = "Seedance 2.5 备用线路";
const changedAt = "2026-11-12T08:15:00Z";
type Segment = DramaFlowFixture["state"]["segments"][number];
type Manifest = { projectId: string; unitId: string; shotId: string; model: string; imageIds: string[]; audioIds: string[] };
type Quote = Manifest & { previewHash: string; requestId: string; resumeChainId: string };
type Attempt = Quote & { taskId: string; status: "failed" | "succeeded" };
const ok = (data: unknown): FixtureResponse => ({ status: 200, body: { code: 0, msg: "ok", data } });
const reject = (msg: string): FixtureResponse => ({ status: 409, body: { code: 409, reason: "journey_state_changed", msg } });

function actor(key: string, title: string) {
    const versionId = `actor-${key}-v1`;
    const representation = { id: versionId, resourceId: `image-${key}`, mediaType: "image/png", role: "primary" };
    return {
        id: `actor-${key}`,
        projectId,
        category: "character",
        title,
        primaryVersionId: versionId,
        versionCount: 1,
        mediaType: "image",
        usages: [],
        storageKey: `resource:${representation.resourceId}`,
        character: {
            versionId,
            version: 1,
            definition: { name: "阿婆" },
            representations: [representation],
            voice: { profile: { sampleResourceId: `voice-${key}` } },
        },
    };
}

function linked(asset: ReturnType<typeof actor>) {
    return {
        id: `ref-${asset.primaryVersionId}`,
        shotId,
        assetVersionId: asset.primaryVersionId,
        asset,
        role: "reference",
        status: "linked",
        source: "segment",
        position: 0,
        referencedVersion: {
            id: asset.primaryVersionId,
            assetId: asset.id,
            version: 1,
            sampleResourceId: asset.character.voice.profile.sampleResourceId,
            representations: asset.character.representations,
        },
    };
}

export function createDramaJourneyProject() {
    const episodes = createEpisodeProject();
    const library = [actor("old", "旧演员"), actor("new", "新演员")];
    let reference = linked(library[0]);
    let chain: VideoChain | undefined;
    let route: ProjectVideoRoute | null = null;
    const compiles: Manifest[] = [];
    const quotes: Quote[] = [];
    const attempts: Attempt[] = [];
    const requests = new Map<string, VideoChainRequest>();
    let discardDeleted = false;
    const base = `/projects/${projectId}`;
    const chainsPath = `${base}/video-chains`;
    const shotPath = `${base}/shots/${shotId}`;

    function manifest(segment: Segment, model: string): Manifest {
        const version = reference.referencedVersion;
        const speaking = segment.spec.shots.some((shot) => shot.lines.some((line) => line.speaker === reference.asset.character.definition.name));
        return { projectId, unitId: segment.unitId, shotId: segment.shotId, model, imageIds: version.representations.map((rep) => rep.resourceId), audioIds: speaking ? [version.sampleResourceId] : [] };
    }

    function quoteChain(segment: Segment, quote: Quote, seconds: number): VideoChain {
        return {
            projectId,
            unitId: segment.unitId,
            anchorShotId: segment.shotId,
            whiteModelStoryboardEnabled: false,
            scope: "scene",
            status: "preview",
            stage: "",
            previewHash: quote.previewHash,
            totalMicrocredits: 2_000_000,
            steps: [
                {
                    shotId: segment.shotId,
                    revisionId: segment.revisionId,
                    title: segment.title,
                    scene: segment.spec.scene,
                    contentSeconds: segment.durationMs / 1000,
                    generationSeconds: seconds,
                    model: quote.model,
                    status: "waiting",
                    phase: "waiting",
                    ending: false,
                    referenceCount: quote.imageIds.length,
                    referenceAudioCount: quote.audioIds.length,
                    quote: { logicalModelId: "", billingMode: "fixed_request", quantity: 1, amountMicrocredits: 2_000_000, estimated: false },
                },
            ],
        };
    }

    function attach(api: DramaFlowFixture) {
        episodes.attach(api);
        const segment = api.state.segments.find((item) => item.shotId === shotId)!;
        if (!discardDeleted && !api.state.segments.some((item) => item.shotId === "discard-unit")) {
            api.state.segments.push({ ...segment, shotId: "discard-unit", title: "待删空镜", revisionId: "discard-v1", position: segment.position + 1, spec: { ...segment.spec, id: "S02", title: "待删空镜" } });
        }
        const original = api.catalog.channels[0].models.find((item) => item.capability === "video");
        if (!original || !("capabilityConfig" in original) || !original.capabilityConfig?.video) throw new Error("缺少视频目录");
        original.capabilityConfig = structuredClone(original.capabilityConfig);
        original.capabilityConfig.video!.references.maxAudios = 1;
        api.catalog.channels.push({ id: "CHANNEL_000002", displayName: "备用渠道", sortOrder: 1, models: [{ ...original, modelKey: automaticKey, displayName: automaticName }] });
        const fallback = api.handle;
        api.handle = (method, rawPath, body) => {
            const url = new URL(rawPath, "http://fixture.local");
            const path = url.pathname;
            const verb = method.toUpperCase();
            if (verb === "GET" && path === `${base}/assets`) return ok({ assets: library });
            if (verb === "GET" && path === `${base}/drama/video-routing`) return ok({ route });
            if (verb === "GET" && path === chainsPath) return ok({ chains: chain && url.searchParams.get("unitId") === chain.unitId ? [chain] : [] });
            const target = api.state.segments.find((item) => item.unitId === unitId && item.shotId === shotId);
            if (verb === "GET" && path === `${base}/units/${unitId}/workspace`) {
                const result = fallback(method, rawPath, body);
                if (result?.body?.code === 0 && result.body.data) Object.assign(result.body.data, { assets: library, shotReferences: target ? [reference] : [] });
                return result;
            }
            if (verb === "DELETE" && path === `${base}/shots/discard-unit`) {
                if ((body as { expectedRevisionId: string }).expectedRevisionId !== "discard-v1") return reject("要删除的分镜版本已变");
                discardDeleted = true;
                const index = api.state.segments.findIndex((item) => item.shotId === "discard-unit");
                if (index >= 0) api.state.segments.splice(index, 1);
                return ok({ deleted: index >= 0 });
            }
            if (verb === "POST" && path === `${shotPath}/assets`) {
                const input = body as { assetVersionId: string; replaceReferenceId: string };
                const selected = library.find((asset) => asset.primaryVersionId === input.assetVersionId);
                if (!target || !selected || reference.id !== input.replaceReferenceId) return reject("绑定对象已变");
                reference = linked(selected);
                return ok({ reference });
            }
            if (verb === "POST" && path === `${shotPath}/compile`) {
                if (!target) return reject("分镜已删除");
                const media = manifest(target, (body as { channelModel: string }).channelModel);
                compiles.push(media);
                const versionId = reference.assetVersionId;
                const voiceBindings = media.audioIds.map((sampleResourceId, i) => ({ versionId, sampleResourceId, tag: `@音频${i + 1}` }));
                const files = [{ file: versionId, tag: "@图片1", role: "identity" }, ...voiceBindings.map((voice) => ({ file: voice.sampleResourceId, tag: voice.tag, role: "voice" }))];
                return ok({
                    compiled: {
                        shotId,
                        spec: target.spec,
                        derived: false,
                        voiceBindings,
                        result: { segment: target.spec.id, duration: target.spec.dur, prompt: target.spec.shots[0].text, chars: target.spec.shots[0].text.length, model: media.model, files, issues: [] },
                    },
                });
            }
            if (verb === "POST" && (path === chainsPath || path === chainsPath + "/preview" || path === `${chainsPath}/${chainId}/retry`)) {
                const input = body as VideoChainRequest;
                if (!target || input.unitId !== unitId || input.shotId !== shotId || !input.requestId) return reject("生成目标与本项目分镜不符");
                const media = manifest(target, input.model.model);
                const config = input.model.input.config as Record<string, unknown>;
                const seconds = Number(config.videoSeconds);
                if (media.model !== (route?.model || FIXTURE_VIDEO_MODEL)) return reject("项目已换线，请重新报价");
                if (path.endsWith("/preview")) {
                    if ((input.resumeChainId || "") !== (chain?.id || "")) return reject("恢复的生成链不匹配");
                    const quote = { ...media, requestId: input.requestId, resumeChainId: input.resumeChainId || "", previewHash: `journey-quote-${quotes.length + 1}` };
                    quotes.push(quote);
                    requests.set(quote.previewHash, structuredClone(input));
                    return ok({ chain: quoteChain(target, quote, seconds) });
                }
                const quote = quotes.find((item) => item.previewHash === input.previewHash);
                const { previewHash, ...confirmed } = input;
                if (!quote || JSON.stringify(requests.get(previewHash!)) !== JSON.stringify(confirmed) || JSON.stringify([quote.imageIds, quote.audioIds]) !== JSON.stringify([media.imageIds, media.audioIds])) return reject("报价或绑定已更新");
                if (quote.resumeChainId ? path !== `${chainsPath}/${chainId}/retry` : path !== chainsPath) return reject("生成入口与报价不符");
                if (attempts.some((item) => item.requestId === input.requestId)) return ok({ chain });
                const failed = quote.model === FIXTURE_VIDEO_MODEL;
                const attempt: Attempt = { ...quote, taskId: `journey-attempt-${attempts.length + 1}`, status: failed ? "failed" : "succeeded" };
                attempts.push(attempt);
                chain = quoteChain(target, quote, seconds);
                Object.assign(chain, { id: chainId, status: attempt.status, stage: failed ? "渠道不可用" : "完成" });
                Object.assign(chain.steps[0], {
                    taskId: attempt.taskId,
                    status: attempt.status,
                    phase: failed ? "generating" : "completed",
                    ...(failed
                        ? {
                              error: "上游 HTTP 503：没有可用线路",
                              diagnostic: { category: "route_unavailable", countDecision: "increment", upstreamCode: "HTTP 503", attemptId: attempt.taskId, terminalStatus: "failed", safeToResubmit: true, canQueryProvider: false },
                          }
                        : { videoResourceId: "journey-result" }),
                });
                if (failed && attempts.length === 3) {
                    route = { status: "auto", fromModel: quote.model, model: automaticModel, reason: "本项目同线路连续三次渠道故障", successRate: 96, sampledAt: changedAt, changedAt, revision: 1 };
                    api.state.videoRoute = route;
                }
                return ok({ chain });
            }
            const result = fallback(method, rawPath, body);
            if (verb === "GET" && path === `${base}/drama/progress` && chain?.status === "succeeded") {
                const progress = (result?.body?.data as { progress?: DramaProgress })?.progress;
                const completed = progress?.segments?.find((item) => item.shotId === shotId);
                if (progress && completed) {
                    Object.assign(completed, { generated: true, takes: 1 });
                    progress.counts.generated = 1;
                }
            }
            return result;
        };
    }
    return { episodes, attach, attempts, quotes, compiles, automaticModel, automaticName, oldActor: library[0].title, newActor: library[1].title };
}
