import assert from "node:assert/strict";
import { test } from "node:test";
import type { DramaColorScriptEntry } from "@/services/api/project-drama";
import type { ProjectAsset } from "@/services/api/projects";
import { paletteSceneGroups } from "./palette-episodes";

function entry(episode: number, unit_id?: string): DramaColorScriptEntry {
    return { episode, unit_id, scene_no: 4, scene: "内·库房·夜", inherits_from: "E01S01", shared_colors: [], new_colors: [] };
}

function card(id: string, record: DramaColorScriptEntry): ProjectAsset {
    return {
        id,
        title: id,
        mediaType: "image",
        category: "palette",
        status: "ready",
        versionCount: 1,
        usages: [],
        position: 0,
        updatedAt: "",
        palette: { entry: record, code: `E0${record.episode}S04`, design: "", imageStatus: "bound" },
    };
}

test("删集中间集后，色卡和色彩脚本使用现序；已删集和匿名旧卡各自保留原编号", () => {
    const episodes = [
        { id: "first", title: "入站" },
        { id: "third", title: "归途" },
    ];
    const script = [entry(3, "third"), entry(2, "deleted"), entry(2), entry(1, "first")];
    const assets = script.map((record, index) => card(`card${index}`, record));
    const groups = paletteSceneGroups(episodes, assets, script);
    assert.deepEqual(
        groups.map((group) => [group.key, group.number, group.assets[0]?.id, group.entries[0]?.unit_id]),
        [
            ["episode:first", 1, "card3", "first"],
            ["episode:third", 2, "card0", "third"],
            ["outside", undefined, "card1", "deleted"],
            ["legacy", undefined, "card2", undefined],
        ],
    );
    assert.equal(groups[1]!.entries[0]!.episode, 3);
    assert.equal(groups[1]!.assets[0]!.palette!.code, "E03S04");
    assert.equal(groups[2]!.entries[0]!.episode, 2);
    assert.equal(groups[3]!.entries[0]!.episode, 2);
});

test("剧集列表未读到时无法确认来源，已读到空列表时才显示为历史色卡", () => {
    const address = entry(3, "existing");
    assert.equal(paletteSceneGroups(undefined, [], [address])[0]?.title, "剧集顺序未读到");
    assert.match(paletteSceneGroups([], [], [address])[0]!.title, /来源不在当前剧集/);
});
