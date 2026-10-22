import { describe, expect, test } from "bun:test";

import { buildAssetChecklist, checklistSummary, draftBibleFromAssets, setChecklistChecked } from "@/pages/projects/detail/drama-asset-checklist";
import type { DramaBible } from "@/services/api/drama";
import type { ProjectAsset } from "@/services/api/projects";

function asset(patch: Partial<ProjectAsset> & { id: string; title: string; category: ProjectAsset["category"] }): ProjectAsset {
    return { mediaType: "image", status: "ready", versionCount: 1, usages: [], position: 0, updatedAt: "", ...patch } as ProjectAsset;
}

const lockCard = ["齐耳短发", "左眉一颗痣", "护士服左胸别两支笔", "圆框眼镜", "工牌挂绳蓝色", "白色护士鞋", "右手腕黑色皮筋", "口罩挂在下巴"];

describe("素材清单：机器能查的门禁", () => {
    test("人物造型要三视图、8～12 项锁定卡、有台词时要音色，全过才可勾", () => {
        const bible: DramaBible = { characters: [{ name: "林小满", variants: [{ label: "默认", sheet: "v-lin", lock_card: lockCard, voice: "voice-lin" }] }] };
        const [item] = buildAssetChecklist(bible, [asset({ id: "a1", title: "林小满", category: "character", primaryVersionId: "v-lin" })]);
        expect(item.checks.map((check) => check.label)).toEqual(["三视图", "锁定卡", "音色"]);
        expect(item.ready).toBe(true);
        expect(item.asset?.id).toBe("a1");
        expect(item.variant).toBeUndefined();
    });

    test("锁定卡太短、没音色时列出缺口，不可勾", () => {
        const bible: DramaBible = { characters: [{ name: "七床", variants: [{ label: "受伤后", sheet: "v-7", lock_card: ["病号服"] }] }] };
        const [item] = buildAssetChecklist(bible, []);
        expect(item.ready).toBe(false);
        expect(item.checks.filter((check) => !check.ok).map((check) => check.label)).toEqual(["锁定卡", "音色"]);
        expect(item.variant).toBe("受伤后");
        expect(item.asset).toBeUndefined();
    });

    test("不说话的角色不要求音色；角色卡已绑定的形象和声音也算数", () => {
        const bible: DramaBible = { characters: [{ name: "路人", speaks: false, variants: [{ label: "默认", lock_card: lockCard }] }, { name: "林小满", variants: [{ label: "默认", lock_card: lockCard }] }] };
        const character = { versionId: "v", version: 1, definition: {}, representations: [], visualStatus: "ready", voiceStatus: "ready" };
        const items = buildAssetChecklist(bible, [asset({ id: "a1", title: "路人", category: "character", character }), asset({ id: "a2", title: "林小满", category: "character", character })]);
        expect(items[0].checks.map((check) => check.label)).toEqual(["三视图", "锁定卡"]);
        expect(items.every((item) => item.ready)).toBe(true);
    });

    test("场景要无人空景，道具要道具图", () => {
        const bible: DramaBible = { locations: [{ name: "护士站", plate: "v-station" }, { name: "七床病房" }], props: [{ name: "输液袋" }] };
        const items = buildAssetChecklist(bible, []);
        expect(items.map((item) => [item.name, item.ready])).toEqual([["护士站", true], ["七床病房", false], ["输液袋", false]]);
    });
});

describe("素材清单：写回连戏表", () => {
    const bible: DramaBible = {
        characters: [{ name: "林小满", variants: [{ label: "默认" }, { label: "下班后", checked: true }] }],
        locations: [{ name: "护士站", plate: "v1" }],
        props: [{ name: "输液袋", image: "v2" }],
        timeline: ["第一夜"],
    };

    test("只改对应条目的 checked，其他字段原样保留", () => {
        const next = setChecklistChecked(setChecklistChecked(bible, "character:0:0", true), "prop:0", true);
        expect(next.characters?.[0].variants.map((variant) => variant.checked)).toEqual([true, true]);
        expect(next.props?.[0].checked).toBe(true);
        expect(next.locations?.[0].checked).toBeUndefined();
        expect(next.timeline).toEqual(["第一夜"]);
        expect(bible.characters?.[0].variants[0].checked).toBeUndefined();
    });

    test("统计已过门禁与文件已齐的条数", () => {
        const summary = checklistSummary(buildAssetChecklist(bible, []));
        expect(summary).toEqual({ total: 4, checked: 1, ready: 2 });
    });
});

describe("按已确认资产起草清单", () => {
    test("角色、场景、道具各成一项，文件写资产版本 ID，checked 一律 false", () => {
        const draft = draftBibleFromAssets({ timeline: ["第一夜"] }, [
            asset({ id: "a1", title: "林小满", category: "character", primaryVersionId: "v-lin", character: { versionId: "v", version: 1, definition: {}, representations: [], visualStatus: "ready", voiceStatus: "missing" } }),
            asset({ id: "a2", title: "护士站", category: "environment", primaryVersionId: "v-station" }),
            asset({ id: "a3", title: "输液袋", category: "prop", primaryVersionId: "v-bag" }),
            asset({ id: "a4", title: "参考图", category: "material", primaryVersionId: "v-ref" }),
        ]);
        expect(draft.characters).toEqual([{ name: "林小满", variants: [{ label: "默认", sheet: "v-lin", checked: false }] }]);
        expect(draft.locations).toEqual([{ name: "护士站", plate: "v-station", checked: false }]);
        expect(draft.props).toEqual([{ name: "输液袋", image: "v-bag", checked: false }]);
        expect(draft.timeline).toEqual(["第一夜"]);
    });
});
