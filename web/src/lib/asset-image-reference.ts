import type { Asset, NewAsset } from "@/stores/use-asset-store";
import { resourceIdFromStorageKey } from "@/services/api/resources";

export function assetImageReferences(asset: Asset | NewAsset) {
    const rank = (role: string) => ({ primary: 0, turnaround_sheet: 1, front: 2 })[role] ?? 3;
    if (asset.imageReferences) return [...asset.imageReferences].sort((a, b) => rank(a.role) - rank(b.role));
    const key = asset.kind === "image" ? asset.data.storageKey : asset.kind === "entity" ? asset.data.coverStorageKey : undefined;
    const resourceId = resourceIdFromStorageKey(key);
    return resourceId ? [{ role: "primary", resourceId }] : [];
}

export function assetImageDisabledReason(asset: Asset | NewAsset) {
    if (asset.status === "archived") return "素材在回收站，请先恢复";
    if (assetImageReferences(asset).length) return undefined;
    if (asset.kind === "image" && (asset.data.storageKey || asset.data.dataUrl || asset.coverUrl)) return undefined;
    return asset.kind === "image" ? "图片文件已不存在" : "该资产暂无可用图片";
}
