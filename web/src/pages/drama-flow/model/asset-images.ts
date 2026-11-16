import type { DramaImageQueueResult } from "@/services/api/project-drama";
import type { ProjectAsset } from "@/services/api/projects";

/** 还没出图、也没在排队的角色、场景、道具（色卡不在此列）：「继续：角色场景资产」的确认面板列它们，规则同旧工作台资产页。 */
export function pendingAssetImages(assets: ProjectAsset[]) {
    return assets.filter((asset) => asset.category !== "palette" && asset.image && asset.image.status !== "bound" && asset.image.status !== "queued");
}

/** 出图排队结果的提示：有没排上的先警告（原因写在卡片上）；waiting 是等主卡、道具出完再接着排的。 */
export function assetImageQueueMessage(result: Pick<DramaImageQueueResult, "queued" | "waiting" | "failed">): { level: "warning" | "info" | "success"; text: string } {
    if (result.failed.length) return { level: "warning", text: `有 ${result.failed.length} 个资产没排上出图，原因写在卡片上` };
    if (!result.queued.length && !result.waiting.length) return { level: "info", text: "没有要出图的资产" };
    if (result.waiting.length) return { level: "success", text: `已排 ${result.queued.length} 个资产出图，${result.waiting.length} 个等前面的（如场景要用的道具）出完再接着排` };
    return { level: "success", text: `已排 ${result.queued.length} 个资产出图` };
}
