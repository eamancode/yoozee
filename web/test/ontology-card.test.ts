import { describe, expect, test } from "bun:test";

import { directorCard, episodePart, ontologyDirectorCard, sssDirectorCard } from "@/lib/ontology-card";

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

    test("accepts the heading spacing the backend accepts and matches unit ids exactly", () => {
        const spaced = "###  单元卡  【s01-u1】｜总时长 12 秒｜2 镜｜节奏短剧档\n【导演分镜卡】\n甲\n【粘贴提示词】\n```\nx\n```\n### 单元卡 s01-u10｜总时长 9 秒｜1 镜｜节奏短剧档\n【导演分镜卡】\n乙";
        expect(ontologyDirectorCard(spaced, "s01-u1")).toContain("甲");
        expect(ontologyDirectorCard(spaced, "s01-u1")).not.toContain("乙");
        expect(ontologyDirectorCard(spaced, "s01-u10")).toContain("乙");
        expect(ontologyDirectorCard("### 单元卡 s01-u10｜总时长 9 秒\n【导演分镜卡】\n乙", "s01-u1")).toBe("");
    });

    test("returns empty for unknown or missing units", () => {
        expect(ontologyDirectorCard(episode, "s09-u1")).toBe("");
        expect(ontologyDirectorCard(episode, "")).toBe("");
        expect(ontologyDirectorCard("", "s01-u1")).toBe("");
    });
});

const sssEpisode = [
    "## 第五部分　单元卡",
    "",
    "### E2-U1｜长档｜叙事 22 秒＋定格 1 秒＝23 秒｜10 镜＋定格｜周家餐厅（本场第 1 段／共 2 段）｜钩子",
    "",
    "**导演阐述：** 门帘下的鞋。",
    "",
    "**粘贴提示词：**",
    "```",
    "场景背景",
    "约23秒。",
    "```",
    "",
    "### E2-U2｜长档｜叙事 22 秒＋定格 1 秒＝23 秒｜10 镜＋定格｜周家餐厅（本场第 2 段／共 2 段）｜卡点",
    "",
    "#### 导演阐述",
    "桌子底下。",
    "",
    "#### 粘贴提示词",
    "```",
    "场景背景",
    "```",
    "",
    "## 第六部分　后期备忘",
    "剪掉定格。",
    "",
    "## 第八部分　复看记录与待确认",
    "",
    "### 待确认",
    "1. 周母的称呼。",
].join("\n");

describe("sssDirectorCard", () => {
    test("cuts each SSS unit card before its paste prompt, for both label styles", () => {
        const u1 = sssDirectorCard(sssEpisode, "E2-U1");
        expect(u1).toStartWith("### E2-U1｜");
        expect(u1).toContain("门帘下的鞋");
        expect(u1).not.toContain("粘贴提示词");
        const u2 = directorCard("sss", sssEpisode, "E2-U2");
        expect(u2).toContain("桌子底下");
        expect(u2).not.toContain("场景背景");
        expect(sssDirectorCard(sssEpisode, "E2-U3")).toBe("");
        expect(directorCard("ontology", episode, "s01-u1")).toContain("事件链：递表");
    });

    test("reads one part of the eight-part document", () => {
        expect(episodePart(sssEpisode, "第八部分")).toContain("周母的称呼");
        expect(episodePart(sssEpisode, "第六部分")).toBe("剪掉定格。");
        expect(episodePart(sssEpisode, "第七部分")).toBe("");
    });
});
