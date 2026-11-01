import { describe, expect, test } from "bun:test";

import { splitTextIntoChapters } from "@/lib/canvas/canvas-document";
import { detectProjectSource, formatSourceChars, plainTextToHtml, sourceDetectionLabel, suggestProjectName } from "@/lib/project-source";

const novelParagraph = "夜班的走廊总是比白天长，日光灯一盏接一盏地亮下去，林小满推着药车从护士站出来，车轮在水磨石地面上发出细碎的声响。她数着门牌往前走，七床的门虚掩着，里面没有开灯，只有监护仪的屏幕亮着一小块绿光。";

describe("detectProjectSource", () => {
    test("一句话创意", () => {
        const result = detectProjectSource("一个失忆的快递员每天收到十年前的信");
        expect(result.kind).toBe("idea");
        expect(result.units).toBe(0);
        expect(sourceDetectionLabel(result, 5)).toBe("识别为 创意 · 将生成 5 集");
    });

    test("带场次号、△ 和「人物：台词」的是剧本", () => {
        const text = ["第1场 内·病房走廊·夜", "△ 日光灯一盏接一盏。", "林小满：七床？", "周岸：小满。"].join("\n");
        const result = detectProjectSource(text);
        expect(result.kind).toBe("script");
        expect(result.units).toBe(1);
        expect(sourceDetectionLabel(result, 5)).toMatch(/^识别为 剧本 · 1 集 · \d+ 字$/);
    });

    test("短剧本也按格式认，不当创意", () => {
        expect(detectProjectSource("△ 门开了。\n林小满：谁？").kind).toBe("script");
        expect(detectProjectSource("## 内·电梯里·夜\n周岸：退后！").kind).toBe("script");
    });

    test("没有场次头但满是台词的也是剧本", () => {
        const text = ["林小满：七床？", "周岸：小满。", "林小满（小声）：你怎么在这？", "周岸：先别说话。"].join("\n");
        expect(detectProjectSource(text).kind).toBe("script");
    });

    test("剧本的集数和章节页导入切出来的一样", () => {
        const text = ["第一集 雨夜来信", "## 内·病房走廊·夜", "林小满：七床？", "", "第二集 灯塔以北", "## 外·码头·晨", "周岸：走吧。", "", "第三集 刀", "△ 防火门被撞开。"].join("\n");
        const result = detectProjectSource(text);
        expect(result.kind).toBe("script");
        expect(result.units).toBe(splitTextIntoChapters(text).length);
        expect(result.units).toBe(3);
    });

    test("长篇连续叙事是小说，章数按章标题切", () => {
        const text = ["第一章 七床空了", novelParagraph, novelParagraph, "第二章 电梯口", novelParagraph, novelParagraph].join("\n");
        const result = detectProjectSource(text);
        expect(result.kind).toBe("novel");
        expect(result.units).toBe(2);
        expect(sourceDetectionLabel(result, 5)).toMatch(/^识别为 小说 · 2 章 · /);
    });

    test("小说里偶尔有「他说：」不算剧本", () => {
        const text = [novelParagraph, "她说：“七床？”", novelParagraph, novelParagraph, novelParagraph].join("\n");
        expect(detectProjectSource(text).kind).toBe("novel");
    });

    test("超过约 300 字、没有结构的是小说", () => {
        expect(detectProjectSource(novelParagraph.repeat(4)).kind).toBe("novel");
    });
});

describe("formatSourceChars", () => {
    test("一万以下带千分位，一万以上写万字", () => {
        expect(formatSourceChars(12480)).toBe("1.2 万字");
        expect(formatSourceChars(9_480)).toBe("9,480 字");
        expect(formatSourceChars(96_000)).toBe("9.6 万字");
    });
});

describe("suggestProjectName", () => {
    test("文件名优先，其次剧本标题或首句", () => {
        expect(suggestProjectName("正文", "长安夜行.txt")).toBe("长安夜行");
        expect(suggestProjectName("《雨夜来信》\n第1场")).toBe("雨夜来信");
        expect(suggestProjectName("一个失忆的快递员每天收到十年前的信，信里写着他明天会做的事。")).toBe("一个失忆的快递员每天收到十年前的信");
    });
});

describe("plainTextToHtml", () => {
    test("空行分段、换行转 br、转义尖括号", () => {
        expect(plainTextToHtml("a\nb\n\n<c>")).toBe("<p>a<br>b</p><p>&lt;c&gt;</p>");
    });
});
