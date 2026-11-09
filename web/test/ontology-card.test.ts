import { describe, expect, test } from "bun:test";

import { ontologyDirectorCard } from "@/lib/ontology-card";

const episode = [
    "## 五、单元卡",
    "### 单元卡 s01-u1｜总时长 12 秒（叙事 11 秒 + 交接 1 秒）｜2 镜｜节奏短剧档",
    "",
    "【导演分镜卡】",
    "事件链：递表 → 认出",
    "",
    "【粘贴提示词】",
    "```",
    "12秒，竖屏9:16。本段不使用背景音乐。",
    "```",
    "",
    "### 单元卡 s01-u2｜总时长 10 秒（叙事 9 秒 + 交接 1 秒）｜2 镜｜节奏短剧档",
    "【导演分镜卡】",
    "事件链：追问",
    "【粘贴提示词】",
    "```",
    "10秒。",
    "```",
    "",
    "## 六、本集交接卡",
    "【集交接卡】",
].join("\n");

describe("ontologyDirectorCard", () => {
    test("returns the director card of the requested unit without the paste prompt", () => {
        const card = ontologyDirectorCard(episode, "s01-u1");
        expect(card.startsWith("### 单元卡 s01-u1")).toBe(true);
        expect(card).toContain("事件链：递表 → 认出");
        expect(card).not.toContain("【粘贴提示词】");
        expect(card).not.toContain("s01-u2");
    });

    test("stops at the next unit or section", () => {
        const card = ontologyDirectorCard(episode, "s01-u2");
        expect(card).toContain("事件链：追问");
        expect(card).not.toContain("集交接卡");
    });

    test("returns empty for unknown or missing units", () => {
        expect(ontologyDirectorCard(episode, "s09-u1")).toBe("");
        expect(ontologyDirectorCard(episode, "")).toBe("");
        expect(ontologyDirectorCard("", "s01-u1")).toBe("");
    });
});
