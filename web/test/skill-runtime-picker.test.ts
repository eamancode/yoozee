import { afterEach, describe, expect, test } from "bun:test";

import { hasRememberedSkillIds, readRememberedSkillIds } from "../src/components/skills/skill-runtime-picker";

const originalWindow = (globalThis as { window?: unknown }).window;

function withLocalStorage(entries: Record<string, string>) {
    const store = new Map(Object.entries(entries));
    Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: { localStorage: { getItem: (key: string) => store.get(key) ?? null, setItem: (key: string, value: string) => store.set(key, value), removeItem: (key: string) => store.delete(key) } },
    });
}

afterEach(() => {
    Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
});

describe("技能选择", () => {
    test("按入口和用户 scope 读出上次选的技能，坏数据回落为空", () => {
        withLocalStorage({
            "yingce:active-user-scope": "user-1",
            "yingce:skill-runtime-selection:chapter-storyboard:user:user-1": JSON.stringify(["yz-adapt", "yz-direct", 3]),
            "yingce:skill-runtime-selection:shot-generation:user:user-1": "{bad json",
        });
        expect(readRememberedSkillIds("chapter-storyboard")).toEqual(["yz-adapt", "yz-direct"]);
        expect(readRememberedSkillIds("shot-generation")).toEqual([]);
        expect(readRememberedSkillIds("other")).toEqual([]);
    });

    test("没选过的入口用默认技能，选过（包括清空）就只认用户的", () => {
        withLocalStorage({
            "yingce:active-user-scope": "user-1",
            "yingce:skill-runtime-selection:cleared:user:user-1": "[]",
        });
        expect(hasRememberedSkillIds("chapter-storyboard")).toBe(false);
        expect(readRememberedSkillIds("chapter-storyboard", ["yz-chapter-shots"])).toEqual(["yz-chapter-shots"]);
        expect(hasRememberedSkillIds("cleared")).toBe(true);
        expect(readRememberedSkillIds("cleared", ["yz-chapter-shots"])).toEqual([]);
    });

    test("多选下拉选中一项就收起，章节分镜对话框记住技能", async () => {
        const picker = await Bun.file(new URL("../src/components/skills/skill-runtime-picker.tsx", import.meta.url)).text();
        expect(picker).toContain("onSelect={() => setOpen(false)}");
        expect(picker).toContain("onOpenChange={setOpen}");
        const chapters = await Bun.file(new URL("../src/pages/projects/detail/chapters.tsx", import.meta.url)).text();
        expect(chapters).toContain('useRememberedSkillSelection("chapter-storyboard", availableSkills, skillsLoading, defaultStoryboardSkillIds)');
        expect(chapters).toContain('detail.project.type === "short-drama" && !hasRememberedSkillIds("chapter-storyboard") ? [DEFAULT_CHAPTER_STORYBOARD_SKILL_ID]');
    });
});
