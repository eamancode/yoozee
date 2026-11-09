import { describe, expect, test } from "bun:test";
import { assetImageDisabledReason, assetImageReferences } from "@/lib/asset-image-reference";
import { parseAssetRecord } from "@/lib/asset-record";
import { resolveAssetImageResource } from "@/services/asset-image-reference";
import { remoteAssetToPickerItem } from "@/components/assets/asset-library-picker-modal";
import type { Asset } from "@/stores/use-asset-store";

const base = { title: "历史素材", coverUrl: "", tags: [], createdAt: "2026-10-01", updatedAt: "2026-10-01" };
const image: Asset = { ...base, id: "asset-is-not-resource", kind: "image", category: "other", data: { storageKey: "resource:uploaded-file", dataUrl: "", width: 800, height: 600, bytes: 100, mimeType: "image/png" } };

describe("workbench historical image reuse", () => {
    test.each(["uploaded-file", "generated-file"])("uncached %s image resolves the existing resource without uploading or generating", async (id) => {
        const asset: Asset = { ...image, data: { ...image.data, storageKey: `resource:${id}` } };
        const item = remoteAssetToPickerItem(asset, undefined, true, assetImageDisabledReason(asset));
        expect(item.disabledReason).toBeUndefined();
        expect(await resolveAssetImageResource(item.asset!)).toBe(id);
        expect(await resolveAssetImageResource(asset)).not.toBe(asset.id);
    });
    test.each(["character", "environment", "prop"] as const)("%s cards keep only the chosen current representation", async (category) => {
        const asset: Asset = { ...base, id: `card-${category}`, category, kind: "entity", data: { definition: { name: "不可复制的设定" } }, imageReferences: [{ role: "side", resourceId: "side-file" }, { role: "primary", resourceId: "main-file" }] };
        const parsed = parseAssetRecord(asset);
        expect(assetImageReferences(parsed)[0]?.resourceId).toBe("main-file");
        expect(await resolveAssetImageResource(parsed, "side-file")).toBe("side-file");
        await expect(resolveAssetImageResource(parsed, "stale-file")).rejects.toThrow("已变化");
        expect(asset).toEqual(parsed);
    });
    test("an explicitly empty current representation list cannot fall back to an old cover", async () => {
        const asset: Asset = { ...base, id: "empty-card", kind: "entity", data: { definition: {}, coverStorageKey: "resource:old-cover" }, imageReferences: [] };
        expect(assetImageDisabledReason(asset)).toBe("该资产暂无可用图片");
        await expect(resolveAssetImageResource(asset)).rejects.toThrow("暂无可用图片");
    });
    test("archived and missing files stay unavailable", async () => {
        await expect(resolveAssetImageResource({ ...image, status: "archived" })).rejects.toThrow("回收站");
        expect(assetImageDisabledReason({ ...image, data: { ...image.data, storageKey: undefined, dataUrl: "" } })).toBe("图片文件已不存在");
    });
    test("malformed resource references fail at the API boundary", () => {
        expect(() => parseAssetRecord({ ...image, imageReferences: [{ resourceId: 15, role: "primary" }] })).toThrow();
    });
});
