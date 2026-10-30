import { describe, expect, test } from "bun:test";

const read = (path: string) => Bun.file(new URL(`../src/${path}`, import.meta.url)).text();

describe("项目风格引导", () => {
    test("生成章节分段对话框在没锁定项目风格时直接提示，不能提交", async () => {
        const source = await read("pages/projects/detail/chapters.tsx");
        // 分镜按锁定的项目风格写画面；改版前带画风原文的老项目照旧可用。
        expect(source).toContain("const projectStyleMissing = !projectLockedStyle(detail.project.bibleJson).locked && !parseStyleProfile(detail.project.styleProfileJson)?.prompt.trim();");
        expect(source).toContain("okButtonProps={{ disabled: !selectedTextModel || projectStyleMissing || Boolean(segmentRule.problem) || !storyboardStep }}");
        expect(source).toContain("项目还没锁定风格");
    });

    test("首页只有一个建项作曲区，没有启动条和建项目弹窗", async () => {
        const source = await read("pages/projects/index.tsx");
        expect(source).toContain("<ProjectComposer />");
        for (const gone of ["story-launcher", "创建短剧项目", "授权档位", "项目画风", "选中即锁定", "AI 生成章节"]) expect(source).not.toContain(gone);
    });

    test("项目设置有未保存修改时拦截离开；授权档位、项目画风已删，画幅和画面类型只读", async () => {
        const source = await read("pages/projects/detail/settings.tsx");
        expect(source).toContain("const blocker = useBlocker(unsaved);");
        expect(source).toContain('window.addEventListener("beforeunload", beforeUnload);');
        expect(source).toContain("画面类型与画风");
        expect(source).toContain('<Field label="生成模式">');
        expect(source).toContain('<ReadOnlyField label="画幅" value={project.aspectRatio} note="建项后不可改" />');
        for (const gone of ["授权档位", "项目画风", "未校准", "全权", "每步确认"]) expect(source).not.toContain(gone);
    });
});
