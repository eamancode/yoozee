import { useQuery } from "@tanstack/react-query";

import { listProjectAssets, type DramaImageStatus, type ProjectAsset } from "@/services/api/projects";

/** 资产当前的出图状态：资产设计建的角色、场景、道具在 image 上，色卡在 palette 上；别的资产没有。 */
export function dramaImageStatusOf(asset: ProjectAsset): DramaImageStatus | undefined {
    return asset.image?.status || asset.palette?.imageStatus;
}

/**
 * 出图摘要的一句话，按资产当前的出图状态算（任务结果里的排队结果只是收尾那一刻的快照，后面接着排、出完、失败都会变）。
 * unit 是量词（资产「个」、色卡「张」），where 是失败原因写在哪（「资产上」「色卡上」）。
 */
export function dramaImageSummaryText(statuses: DramaImageStatus[], unit: "个" | "张", where: string) {
    const count = (match: (status: DramaImageStatus) => boolean) => statuses.filter(match).length;
    const bound = count((status) => status === "bound");
    const queued = count((status) => status === "queued");
    const waiting = count((status) => status === "waiting");
    const failed = count((status) => status === "failed" || status === "stale");
    const none = count((status) => status === "none");
    if (statuses.length && bound === statuses.length) return `${bound} ${unit}已出图。`;
    return (
        [
            bound ? `${bound} ${unit}已出图` : "",
            queued ? `${queued} ${unit}出图中` : "",
            waiting ? `${waiting} ${unit}等前面的出完接着排` : "",
            failed ? `${failed} ${unit}没出成（原因写在${where}，到资产页点「继续出图」重出）` : "",
            none ? `${none} ${unit}还没出图（到资产页点「继续出图」可以补）` : "",
        ]
            .filter(Boolean)
            .join("，") + "。"
    );
}

/**
 * 侧栏打开时读这些资产当前的出图状态（按 ids 顺序，读不到的不算）；还有排着或出图中的就每 5 秒再读一次。
 * 查询键在 ["project", projectId, "assets"] 下：资产页「继续出图」、步骤收尾刷新项目时一起失效。
 */
export function useDramaImageStatuses(projectId: string, ids: string[], enabled: boolean) {
    const query = useQuery({
        queryKey: ["project", projectId, "assets", "drama-image-status"],
        queryFn: async () => Object.fromEntries((await listProjectAssets(projectId)).assets.map((asset) => [asset.id, dramaImageStatusOf(asset)])) as Record<string, DramaImageStatus | undefined>,
        enabled: enabled && ids.length > 0,
        staleTime: 0,
        refetchInterval: (current) => (ids.some((id) => current.state.data?.[id] === "queued" || current.state.data?.[id] === "waiting") ? 5000 : false),
    });
    if (!query.data) return undefined;
    return ids.map((id) => query.data[id]).filter((status): status is DramaImageStatus => Boolean(status));
}
