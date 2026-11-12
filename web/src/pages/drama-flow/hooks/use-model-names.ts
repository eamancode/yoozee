import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { systemChannelModelChannels } from "@/lib/user-session";
import { getModelCatalog } from "@/services/api/logical-models";
import { decodeChannelModel, useEffectiveConfig, type ModelChannel } from "@/stores/use-config-store";

import { queryReadState, type ReadState } from "../model/read-state";

export type ModelName = { text: string; tone?: "bad" | "warn" };

/**
 * 按模型 ID 在渠道里找展示名。只做展示：存的、报价和提交用的仍是原 ID。
 * 渠道 ID、内部模型 key 不作为展示文案；系统模型没写展示名时用渠道名。
 */
export function resolveModelName(channels: ModelChannel[], id: string): string | undefined {
    const decoded = decodeChannelModel(id);
    const key = decoded?.model || id;
    const candidates = decoded ? channels.filter((channel) => channel.id === decoded.channelId) : channels;
    for (const channel of candidates) {
        if (!channel.enabled || !channel.models.includes(key)) continue;
        const named = channel.modelCosts?.find((item) => item.model === key)?.displayName?.trim();
        if (named) return named;
        return channel.scope === "system" ? channel.name || "系统模型" : `${key}（${channel.name}）`;
    }
    return undefined;
}

/**
 * 模型 ID → 给人看的名字和状态：没配、在读目录、目录没读到、已不可用分开写。
 * 系统模型以本次读到的模型目录为准（只含可用模型）；个人渠道看本地配置。
 * 目录没读到时不说「已不可用」：能从本地缓存认出名字就写名字并注明没法确认。
 */
export function modelNameOf(input: { id?: string; catalogRead: ReadState; catalogChannels: ModelChannel[]; localChannels: ModelChannel[]; unset: string }): ModelName {
    const id = input.id?.trim();
    if (!id) return { text: input.unset };
    const personal = input.localChannels.filter((channel) => channel.scope !== "system");
    const ownName = resolveModelName(personal, id);
    if (ownName) return { text: ownName };
    if (input.catalogRead === "ready") {
        const name = resolveModelName(input.catalogChannels, id);
        return name ? { text: name } : { text: "原来选的模型已不可用，到设置里重选", tone: "bad" };
    }
    const cached = resolveModelName(input.localChannels, id);
    if (input.catalogRead === "loading") return { text: cached ? `${cached}（正在核对是否可用…）` : "正在读模型目录…" };
    return { text: cached ? `${cached}（模型目录没读到，没法确认现在是否可用）` : "模型目录没读到", tone: "warn" };
}

/** 读一次模型目录（打开时才读），返回按 ID 取名字的函数。 */
export function useModelNames(enabled: boolean) {
    const config = useEffectiveConfig();
    const catalogQuery = useQuery({ queryKey: ["model-catalog"], queryFn: getModelCatalog, enabled, staleTime: 60_000 });
    // 形状不对的目录响应当成没读到，不能当成「所有模型都不可用」。
    const malformed = Boolean(catalogQuery.data) && (catalogQuery.data?.source !== "system" || !Array.isArray(catalogQuery.data?.channels));
    const catalogRead: ReadState = malformed ? "error" : queryReadState(catalogQuery);
    const catalogChannels = useMemo(() => {
        const channels = catalogQuery.data?.channels;
        return !malformed && Array.isArray(channels) ? systemChannelModelChannels(channels) : [];
    }, [catalogQuery.data, malformed]);
    const nameOf = (id: string | undefined, unset: string) => modelNameOf({ id, catalogRead, catalogChannels, localChannels: config.channels, unset });
    return { nameOf, catalogRead, refetch: () => catalogQuery.refetch() };
}
