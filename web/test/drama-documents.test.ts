import { describe, expect, test } from "bun:test";

import { applyBriefFacts, bibleSummary, briefConfirmedCount, briefFactsFromDocument, parseDocumentJson } from "../src/pages/projects/detail/drama-brief";

describe("需求文档与连戏表的前端入口", () => {
    test("六项事实读成表单，旧的字符串写法回落为默认待确认", () => {
        const facts = briefFactsFromDocument({ purpose: { value: "抖音投流", status: "已确认", quote: "要投流" }, platform: "竖屏 9:16" });
        expect(facts.purpose).toEqual({ value: "抖音投流", status: "已确认" });
        expect(facts.platform).toEqual({ value: "竖屏 9:16", status: "默认，待确认" });
        expect(facts.budget).toEqual({ value: "", status: "默认，待确认" });
    });

    test("写回时保留引用和其余字段，空值整项去掉", () => {
        const document = { title: "两江一城", purpose: { value: "旧", status: "待答", quote: "原话" }, budget: { value: "500", status: "已确认" }, items: [{ id: "R-001", content: "片头要快", status: "待处理" }] };
        const facts = briefFactsFromDocument(document);
        facts.purpose = { value: "抖音投流", status: "已确认" };
        facts.budget = { value: "  ", status: "已确认" };
        const next = applyBriefFacts(document, facts);
        expect(next.purpose).toEqual({ value: "抖音投流", status: "已确认", quote: "原话" });
        expect(next.budget).toBeUndefined();
        expect(next.items).toEqual(document.items);
        expect(next.title).toBe("两江一城");
    });

    test("确认数只算已确认和默认已授权", () => {
        const facts = briefFactsFromDocument({ purpose: { value: "a", status: "已确认" }, platform: { value: "b", status: "默认，已授权" }, duration: { value: "c", status: "默认，待确认" } });
        expect(briefConfirmedCount(facts)).toBe(2);
    });

    test("JSON 编辑框报可读的错误", () => {
        expect(() => parseDocumentJson("{bad")).toThrow("JSON 格式不对");
        expect(() => parseDocumentJson("[]")).toThrow("文档必须是一个 JSON 对象");
        expect(parseDocumentJson("")).toEqual({});
    });

    test("连戏表摘要数出人物、道具、场景和没过门禁的项", () => {
        expect(bibleSummary({ characters: [{ name: "苏念", variants: [{ label: "默认", checked: true }, { label: "婚纱" }] }], props: [{ name: "戒指" }], locations: [{ name: "医院", checked: true }] })).toEqual({
            characters: 1,
            props: 1,
            locations: 1,
            unchecked: 2,
        });
        expect(bibleSummary(undefined)).toEqual({ characters: 0, props: 0, locations: 0, unchecked: 0 });
    });

    test("概览终态卡给出打开需求文档、连戏表的入口", async () => {
        const source = await Bun.file(new URL("../src/pages/projects/detail/drama-panels.tsx", import.meta.url)).text();
        expect(source).toContain('const DOCUMENT_BY_END_STATE: Record<string, "brief" | "bible" | undefined> = { brief: "brief", script: "bible", refs: "bible" };');
        expect(source).toContain('putDramaBrief(projectId, mode === "json" ? parseDocumentJson(jsonText) : formDocument())');
        expect(source).toContain("<DramaBibleModal projectId={projectId}");
    });
});

describe("剪辑导出", () => {
    test("门禁跑完刷新项目缓存，静帧时间线不再显示没有可渲染片段", async () => {
        const source = await Bun.file(new URL("../src/lib/plugins/builtin/editor/editor-export.tsx", import.meta.url)).text();
        expect(source).toContain('.finally(() => { if (projectId) void queryClient.invalidateQueries({ queryKey: ["project", projectId] }); });');
        expect(source).toContain("个静帧片段由服务端渲染时合成");
    });
});
