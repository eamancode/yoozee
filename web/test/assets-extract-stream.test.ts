import { describe, expect, test } from "bun:test";

import { assetsExtractStreamFromResult, parseAssetsExtractStream } from "../src/pages/projects/detail/assets-extract-stream";

const marker = (index: number, attempt = 1) => JSON.stringify({ "@turn": { index, total: 2, heading: index === 1 ? "内·病房走廊·夜" : "内·电梯口·夜", attempt } });
const zhou = JSON.stringify({ category: "character", name: "周岸", aliases: ["周哥"], appearance: "三十岁男性，\n条纹病号服", prompt_en: "x", first_scene: 2 });
const hall = JSON.stringify({ category: "scene", name: "病房走廊", aliases: [], appearance: "狭长走廊", prompt_en: "y", first_scene: 1 });
const lin = JSON.stringify({ category: "character", name: "林小满", aliases: "小满、林护士", appearance: "", prompt_en: "", first_scene: "S01", variants: [{ label: "淋湿", kind: "state", scenes: [1] }, { label: "" }] });

describe("资产设计流式视图", () => {
    test("按回合标记分场，重试清掉这一场的半截输出，没写完的最后一行不算", () => {
        const text = [marker(1), hall, marker(2), JSON.stringify({ category: "prop", name: "半截" }), marker(2, 2), zhou, `{"category": "prop", "name": "折`].join("\n");
        const state = parseAssetsExtractStream(text);
        expect(state.total).toBe(2);
        expect(state.scenes.map((scene) => [scene.index, scene.heading, scene.attempt, scene.rows.map((row) => row.name)])).toEqual([
            [1, "内·病房走廊·夜", 1, ["病房走廊"]],
            [2, "内·电梯口·夜", 2, ["周岸"]],
        ]);
        expect(state.scenes[0].rows[0].category).toBe("environment");
        expect(state.scenes[1].rows[0].appearance).toBe("三十岁男性， 条纹病号服");
        expect(state.rowCount).toBe(2);
    });

    test("成功任务的结果正文没有回合标记：按 first_scene 分场，场次头取运行进度", () => {
        const state = assetsExtractStreamFromResult(JSON.stringify({ text: [lin, hall, zhou].join("\n"), dramaRun: { total: 2, scenes: [{ heading: "内·病房走廊·夜" }, { heading: "内·电梯口·夜" }], turns: [] } }));
        expect(state.scenes.map((scene) => [scene.index, scene.heading, scene.rows.map((row) => row.name)])).toEqual([
            [1, "内·病房走廊·夜", ["林小满", "病房走廊"]],
            [2, "内·电梯口·夜", ["周岸"]],
        ]);
        expect(state.scenes[0].rows[0].aliases).toEqual(["小满", "林护士"]);
        // 角色行写的子卡只取变体名（空的不要）。
        expect(state.scenes[0].rows[0].variants).toEqual(["淋湿"]);
    });
});
