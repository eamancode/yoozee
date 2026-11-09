import type { Asset, NewAsset } from "@/stores/use-asset-store";
import { assetImageDisabledReason, assetImageReferences } from "@/lib/asset-image-reference";
import { resourceIdFromStorageKey } from "@/services/api/resources";
import { getImageBlob, uploadImage } from "@/services/image-storage";

export async function resolveAssetImageResource(asset: Asset | NewAsset, selectedResourceId?: string): Promise<string> {
    const reason = assetImageDisabledReason(asset);
    if (reason) throw new Error(reason);
    const refs = assetImageReferences(asset);
    if (selectedResourceId) {
        if (!refs.some((ref) => ref.resourceId === selectedResourceId)) throw new Error("所选图片已变化，请重新选择");
        return selectedResourceId;
    }
    if (refs[0]) return refs[0].resourceId;
    if (asset.kind !== "image") throw new Error("该资产暂无可用图片");
    const blob = asset.data.storageKey ? await getImageBlob(asset.data.storageKey) : null;
    const source = blob || asset.data.dataUrl || asset.coverUrl;
    if (!source) throw new Error("图片文件已不存在");
    const uploaded = await uploadImage(source);
    const resourceId = resourceIdFromStorageKey(uploaded.storageKey);
    if (uploaded.pendingRemoteUpload || !resourceId) throw new Error(`图片未能上传到服务器${uploaded.remoteUploadError ? `：${uploaded.remoteUploadError}` : ""}`);
    return resourceId;
}
