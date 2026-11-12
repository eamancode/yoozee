import type { DramaPipeline, DramaSegmentProgress } from "@/services/api/project-drama";

import { flowStageOfPipeline, type FlowStageKey } from "./stages";

/**
 * 待办收件箱：把散在流水线、资产、分段、视频里的「要人管的事」收成一张清单。
 * must 必须处理（不处理走不下去）；confirm 等你确认（点一下就过）；suggest 建议（可以忽略）。
 * 纯函数；每条待办带着去哪处理（步骤 + 哪一集 + 哪一条），页面只负责跳过去并高亮。
 */
export type InboxGroup = "must" | "confirm" | "suggest";

export type InboxActionKind = "retry_image" | "retry_video" | "edit_segment" | "pick_take" | "continue" | "open" | "add_refs";

export type InboxItem = {
    id: string;
    group: InboxGroup;
    stage: FlowStageKey;
    title: string;
    detail: string;
    action: { kind: InboxActionKind; label: string };
    target: { unitId?: string; itemId?: string };
};

export type InboxAsset = { id: string; title: string; kind: "asset" | "palette"; status: string; error?: string };

export type InboxVideoStep = { chainId: string; unitId: string; shotId: string; title: string; status: string; error?: string };

export type InboxFacts = {
    pipeline?: DramaPipeline;
    segments?: DramaSegmentProgress[];
    assets?: InboxAsset[];
    videoSteps?: InboxVideoStep[];
    /** 用户点过「忽略」的建议（只对 suggest 生效）。 */
    ignored?: string[];
};

const GROUP_ORDER: Record<InboxGroup, number> = { must: 0, confirm: 1, suggest: 2 };

export const INBOX_GROUP_LABEL: Record<InboxGroup, string> = { must: "必须处理", confirm: "等你确认", suggest: "建议" };

export function buildInbox(facts: InboxFacts): InboxItem[] {
    const items: InboxItem[] = [];
    const stop = facts.pipeline?.stop;
    if (stop && stop.stage !== "video" && stop.stage !== "cut") {
        const stage = flowStageOfPipeline(stop.stage) ?? "script";
        items.push({
            id: `stop:${stop.stage}`,
            group: stop.continuable ? "confirm" : "must",
            stage,
            title: stop.continuable ? `「${stop.label}」等你确认后继续` : `「${stop.label}」停住了`,
            detail: stop.reason,
            action: stop.continuable ? { kind: "continue", label: "继续" } : { kind: "open", label: "去处理" },
            target: {},
        });
    }

    for (const asset of facts.assets ?? []) {
        if (asset.status !== "failed" && asset.status !== "stale") continue;
        items.push({
            id: `image:${asset.id}`,
            group: "must",
            stage: "look",
            title: `${asset.kind === "palette" ? "色卡" : "定妆图"}「${asset.title}」没出成`,
            detail: asset.error || (asset.status === "stale" ? "出图期间换了版本，没绑上" : "出图失败"),
            action: { kind: "retry_image", label: "重试这一张" },
            target: { itemId: asset.id },
        });
    }

    const failedShots = new Set<string>();
    for (const step of facts.videoSteps ?? []) {
        if (step.status !== "failed" || failedShots.has(step.shotId)) continue;
        failedShots.add(step.shotId);
        items.push({
            id: `video:${step.shotId}`,
            group: "must",
            stage: "video",
            title: `「${step.title}」视频没出成`,
            detail: step.error || "生成失败",
            action: { kind: "retry_video", label: "重新出（先看花费）" },
            target: { unitId: step.unitId, itemId: step.shotId },
        });
    }

    for (const segment of facts.segments ?? []) {
        const name = segment.title || segment.segmentId;
        const target = { unitId: segment.unitId, itemId: segment.shotId };
        if (segment.overLimit || (segment.issues ?? 0) > 0) {
            items.push({
                id: `segment:${segment.shotId}`,
                group: "must",
                stage: "shots",
                title: `「${name}」${segment.overLimit ? "超出单段时长" : "有问题没改完"}`,
                detail: segment.overLimit ? "这一段太长，模型一次出不了，需要拆短" : `还有 ${segment.issues} 处检查没过`,
                action: { kind: "edit_segment", label: "就地修改" },
                target,
            });
        } else if (!segment.generated && segment.script && !segment.refs) {
            items.push({
                id: `refs:${segment.shotId}`,
                group: "suggest",
                stage: "shots",
                title: `「${name}」没带参考图`,
                detail: "不带参考也能出，但人物和场景容易跑偏",
                action: { kind: "add_refs", label: "去补参考" },
                target,
            });
        }
        if (segment.generated && !failedShots.has(segment.shotId) && segment.takes > 1 && !segment.selectedArtifactId) {
            items.push({
                id: `pick:${segment.shotId}`,
                group: "confirm",
                stage: "video",
                title: `「${name}」有 ${segment.takes} 条视频，等你挑一条`,
                detail: "不挑的话成片默认用最新的一条",
                action: { kind: "pick_take", label: "挑片" },
                target,
            });
        }
    }

    const ignored = new Set(facts.ignored ?? []);
    return items.filter((item) => item.group !== "suggest" || !ignored.has(item.id)).sort((a, b) => GROUP_ORDER[a.group] - GROUP_ORDER[b.group]);
}

export function inboxCounts(items: InboxItem[]) {
    return { must: items.filter((item) => item.group === "must").length, confirm: items.filter((item) => item.group === "confirm").length, suggest: items.filter((item) => item.group === "suggest").length };
}

const ignoreKey = (scope: string, projectId: string) => `drama-flow:ignored:${scope}:${projectId}`;

/** 忽略的建议只是界面偏好，存 localStorage（小、按账号和项目分开）；读坏了当作没忽略过。 */
export function readIgnoredSuggestions(scope: string, projectId: string): string[] {
    try {
        const parsed: unknown = JSON.parse(localStorage.getItem(ignoreKey(scope, projectId)) || "[]");
        return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
    } catch {
        return [];
    }
}

export function writeIgnoredSuggestions(scope: string, projectId: string, ids: string[]) {
    try {
        localStorage.setItem(ignoreKey(scope, projectId), JSON.stringify([...new Set(ids)].slice(-200)));
    } catch {
        // 存不下只影响「忽略」能不能记住，不影响制作。
    }
}
