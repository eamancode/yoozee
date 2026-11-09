import { useMemo, useState } from "react";
import { Select } from "antd";
import { AssetLibraryPickerModal, type AssetLibraryPickerItem } from "./asset-library-picker-modal";
import { AppModal } from "@/components/ui/product/app-modal";
import { AssetMediaPreview } from "@/components/asset-media-preview";
import { CachedResourceImage } from "@/components/cached-resource-image";
import { useExternalAssetSources } from "@/hooks/use-external-asset-sources";
import { ASSET_CATEGORY_LABELS } from "@/lib/asset-category";
import { assetImageDisabledReason, assetImageReferences } from "@/lib/asset-image-reference";
import { resolveAssetImageResource } from "@/services/asset-image-reference";
import { resourceFileUrl } from "@/services/api/resources";
import { useAssetStore } from "@/stores/use-asset-store";

const ROLE_LABELS: Record<string, string> = { primary: "主图", turnaround_sheet: "角色设定图", front: "正面", side: "侧面", back: "背面" };

export function AssetImageLibraryPicker({ open, title, onClose, onBind }: { open: boolean; title: string; onClose: () => void; onBind: (image: { resourceId: string; sourceAssetId: string }) => Promise<unknown> }) {
    const assets = useAssetStore((state) => state.assets);
    const external = useExternalAssetSources(open);
    const [preview, setPreview] = useState<AssetLibraryPickerItem | null>(null);
    const [choices, setChoices] = useState<Record<string, string>>({});
    const close = () => {
        setPreview(null);
        setChoices({});
        onClose();
    };
    const items = useMemo<AssetLibraryPickerItem[]>(
        () => [
            ...assets
                .filter((asset) => (asset.kind === "image" || asset.kind === "entity" || asset.imageReferences?.length) && !assetImageDisabledReason(asset))
                .map((asset) => ({
                    id: asset.id,
                    title: asset.title,
                    category: asset.category || "other",
                    kindLabel: asset.kind === "image" ? "图片" : "资产图片",
                    asset,
                    disabledReason: assetImageDisabledReason(asset),
                    imageStorageKey: assetImageReferences(asset)[0] ? `resource:${assetImageReferences(asset)[0].resourceId}` : undefined,
                })),
            ...external.items.filter((item) => item.external?.item.kind === "image"),
        ],
        [assets, external.items],
    );
    const refs = preview?.asset ? assetImageReferences(preview.asset) : [];
    const previewResource = preview ? choices[preview.id] || refs[0]?.resourceId : undefined;
    return (
        <>
            <AssetLibraryPickerModal
                open={open}
                remoteLibrary
                remoteImageReferences
                allowCharacters
                acceptRemoteItems
                items={items}
                categoryLabels={{ all: "全部素材", ...ASSET_CATEGORY_LABELS, ...external.categoryLabels }}
                folders={external.folders}
                multiple={false}
                title={title}
                eyebrow="绑定图片"
                remoteItemDisabledReason={assetImageDisabledReason}
                confirmLabel={() => "绑定图片并保存新版本"}
                footerNote={external.error || "直接复用已有图片，仅替换目标卡图片；不调用模型，不复制源资产设定。可先预览图片及视角。"}
                onPreview={setPreview}
                onClose={close}
                onConfirm={async (ids, pickedItems) => {
                    const item = pickedItems?.find((entry) => entry.id === ids[0]);
                    if (!item) throw new Error("所选图片未加载，请重新选择");
                    const asset = item.external ? await external.importExternalAsset(item.external) : item.asset;
                    if (!asset) throw new Error("所选素材已不存在");
                    const resourceId = await resolveAssetImageResource(asset, choices[item.id]);
                    await onBind({ resourceId, sourceAssetId: "id" in asset ? asset.id : item.id });
                    close();
                }}
            />
            <AppModal open={open && Boolean(preview)} title={preview ? `预览：${preview.title}` : "图片预览"} footer={null} onCancel={() => setPreview(null)} width={900}>
                {preview && (
                    <>
                        {refs.length > 1 && (
                            <Select
                                className="mb-3 w-full"
                                aria-label="选择该资产的图片视角"
                                value={previewResource}
                                options={Array.from(new Map(refs.map((ref) => [ref.resourceId, { value: ref.resourceId, label: ROLE_LABELS[ref.role] || ref.role }])).values())}
                                onChange={(resourceId) => setChoices((current) => ({ ...current, [preview.id]: resourceId }))}
                            />
                        )}
                        {previewResource ? (
                            <CachedResourceImage storageKey={`resource:${previewResource}`} src={resourceFileUrl(previewResource)} alt={preview.title} eager className="max-h-[70vh] w-full object-contain" />
                        ) : (
                            <AssetMediaPreview asset={preview.asset} alt={preview.title} />
                        )}
                        <p className="mt-3 text-xs text-foreground/60">关闭预览后勾选素材，再确认绑定。仅使用当前图片，不复制源资产的设定或声音。</p>
                    </>
                )}
            </AppModal>
        </>
    );
}
