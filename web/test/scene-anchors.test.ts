import { describe, expect, test } from "bun:test";
import { sceneAnchorList } from "../src/lib/canvas/scene-anchors";

describe("场景锚点", () => {
    test("数组去空去重，一句话按顿号、逗号拆开", () => {
        expect(sceneAnchorList(["电梯门前", " 楼梯间防火门 ", "电梯门前", ""])).toEqual(["电梯门前", "楼梯间防火门"]);
        expect(sceneAnchorList("电梯门前、护士站柜台，楼梯间")).toEqual(["电梯门前", "护士站柜台", "楼梯间"]);
        expect(sceneAnchorList(undefined)).toEqual([]);
    });
});
