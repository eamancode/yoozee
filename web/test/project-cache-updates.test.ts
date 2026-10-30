import { describe, expect, test } from "bun:test";

import type { ProjectAsset, ProjectAssetCandidatePage, ProjectUnit, ProjectUnitWorkspace, ShotAssetReference } from "../src/services/api/projects";
import { appendProjectUnits, applyShotReferenceChange, removeConfirmedCandidate } from "../src/pages/projects/workbench/detail/project-cache";

const unit = (id: string) => ({ id, title: id, position: 0 }) as ProjectUnit;
const reference = (id: string, patch: Partial<ShotAssetReference> = {}) => ({ id, shotId: "shot-1", assetVersionId: `${id}-v1`, role: "reference", status: "linked", createdAt: "2026-10-19T00:00:00Z", ...patch }) as ShotAssetReference;

describe("项目页缓存即时更新", () => {
    test("新建、导入的章节先写进章节索引，重复的不再追加", () => {
        const index = { units: [unit("u1")], canvasCounts: {} };
        expect(appendProjectUnits(index, [unit("u2")])?.units.map((item) => item.id)).toEqual(["u1", "u2"]);
        expect(appendProjectUnits(index, [unit("u1")])).toBe(index);
        expect(appendProjectUnits(undefined, [unit("u2")])).toBeUndefined();
    });

    test("确认后的候选立刻离开待确认列表", () => {
        const page = { candidates: [{ id: "c1" }, { id: "c2" }], page: 1, pageSize: 24, total: 2, hasMore: false } as ProjectAssetCandidatePage;
        const next = removeConfirmedCandidate(page, "c1");
        expect(next?.candidates.map((item) => item.id)).toEqual(["c2"]);
        expect(next?.total).toBe(1);
        expect(removeConfirmedCandidate(page, "missing")).toBe(page);
    });

    test("镜头绑定资产后立刻出现在镜头资产里，取消后立刻移除", () => {
        const workspace = { shotReferences: [reference("r1")] } as ProjectUnitWorkspace;
        const asset = { id: "asset-2", title: "苏念" } as ProjectAsset;
        const added = applyShotReferenceChange(workspace, { added: reference("r2"), asset });
        expect(added?.shotReferences.map((item) => item.id)).toEqual(["r1", "r2"]);
        expect(added?.shotReferences[1].asset?.title).toBe("苏念");
        expect(applyShotReferenceChange(added, { removedId: "r1" })?.shotReferences.map((item) => item.id)).toEqual(["r2"]);
    });

    test("三个写路径都接上了即时缓存", async () => {
        const read = (name: string) => Bun.file(new URL(`../src/pages/projects/workbench/detail/${name}`, import.meta.url)).text();
        expect(await read("chapters.tsx")).toContain("appendProjectUnits(current, [unit])");
        expect(await read("assets.tsx")).toContain("removeConfirmedCandidate(current, variables.candidateId)");
        expect(await read("workflow-production-workbench.tsx")).toContain("applyShotReferenceChange(current,");
    });
});
