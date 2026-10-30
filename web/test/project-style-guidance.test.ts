import { describe, expect, test } from "bun:test";

const read = (path: string) => Bun.file(new URL(`../src/${path}`, import.meta.url)).text();

describe("项目画风引导", () => {
    test("生成章节分段对话框在缺画风时直接提示并给入口，不能提交", async () => {
        const source = await read("pages/projects/workbench/detail/chapters.tsx");
        // 提示词在服务端组装，画风原文取项目保存的画风快照。
        expect(source).toContain("const projectStyleMissing = !parseStyleProfile(detail.project.styleProfileJson)?.prompt.trim();");
        expect(source).toContain("okButtonProps={{ disabled: !selectedTextModel || projectStyleMissing || Boolean(segmentRule.problem) || !storyboardStep }}");
        expect(source).toContain("去选画风");
    });

    test("建项目对话框不再把画风写成纯可选", async () => {
        const source = await read("pages/projects/workbench/index.tsx");
        expect(source).not.toContain("选择项目画风（可选）");
        expect(source).toContain("生成分镜前必选");
    });

    test("项目设置有未保存修改时拦截离开并提示保存画风", async () => {
        const source = await read("pages/projects/workbench/detail/settings.tsx");
        expect(source).toContain("const blocker = useBlocker(unsaved);");
        expect(source).toContain('window.addEventListener("beforeunload", beforeUnload);');
        expect(source).toContain("画风已选好，保存设置后才对分镜和生成生效。");
    });
});
