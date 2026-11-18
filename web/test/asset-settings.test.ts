import { expect, spyOn, test } from "bun:test";

import { availableBindingAssets } from "@/pages/drama-flow/model/shot-bindings";
import { updateProjectAssetSettings, type ProjectAsset } from "@/services/api/projects";
import { http } from "@/services/api/request";

const scene: ProjectAsset = {
    id: "scene-1",
    title: "毛纺厂走廊",
    category: "environment",
    mediaType: "image",
    status: "confirmed",
    primaryVersionId: "settings-draft",
    confirmedImageVersionId: "confirmed-image",
    settingsImageOutdated: true,
    versionCount: 3,
    usages: ["reference"],
    position: 0,
    updatedAt: "2026-11-17T08:00:00Z",
};

test("设定保存提交原版本并读取响应中的资产", async () => {
    const patch = spyOn(http, "patch").mockResolvedValue({ asset: scene });
    try {
        const fields = { description: "青砖回廊，右侧三扇窗" };
        const asset = await updateProjectAssetSettings("project-1", scene.id, "confirmed-image", fields);
        expect(patch).toHaveBeenCalledWith("/projects/project-1/assets/scene-1", { settings: { expectedVersionId: "confirmed-image", fields } });
        expect(asset.id).toBe(scene.id);
        expect(asset.confirmedImageVersionId).toBe("confirmed-image");
    } finally {
        patch.mockRestore();
    }
});

test("当前镜头已绑定已确认图文时，编辑后的草稿不出现在可换版本中", () => {
    expect(availableBindingAssets([scene], [{ id: "ref-1", asset: scene, title: scene.title, kind: "scene", versionId: "confirmed-image", versionIds: ["confirmed-image"], preview: "", sampleResourceId: "" }])).toEqual([]);
});
