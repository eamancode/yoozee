import { expect, test } from "bun:test";
import { parseStyleLockStream } from "../../src/pages/projects/workbench/detail/style-lock-stream";

test("director output exposes original visual fields alongside new director fields", () => {
    const profile = { title: "雨夜写实", description: "完整公共字段", tags: ["写实"], prompt: "【视觉媒介】仿真人\n【角色设计系统】自然肤质", negativePrompt: "禁止塑料皮肤" };
    const row = JSON.stringify({ style_profile: profile, style_anchor: { medium: "仿真人", camera: "克制推镜" } });
    const parsed = parseStyleLockStream(row);
    expect(parsed.profile).toEqual(profile);
    expect(parsed.anchor?.camera).toBe("克制推镜");
    const retried = parseStyleLockStream(row + '\n{"@turn":{"attempt":2}}\n{"style_anchor":{"camera":"新规格"}}');
    expect(retried.profile).toBeUndefined();
    expect(retried.anchor?.camera).toBe("新规格");
});
