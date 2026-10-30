import { expect, test } from "bun:test";

test("章节页直接生成分段到分镜制作，技能由项目风格档案绑定、提示词在服务端组装、镜头表由服务端写", async () => {
    const source = await Bun.file(new URL("../src/pages/projects/workbench/detail/chapters.tsx", import.meta.url)).text();
    expect(source).not.toContain("导入分镜");
    expect(source).not.toContain("importStoryboardToCanvas");
    expect(source).not.toContain("在画布中分镜");
    expect(source).toContain("生成分段");
    // 生成分段的结果由服务端在任务收尾时写镜头表和段，页面只订阅任务流、显示进度。
    expect(source).not.toContain("replaceProjectUnitShots");
    expect(source).not.toContain("putDramaShotlist");
    expect(source).toContain("<ChapterStoryboardRunPanel");
    expect(source).toContain("/video`");
    expect(source).not.toContain("SkillRuntimePicker");
    expect(source).toContain('getDramaStepUsage(detail.project.id, "chapter.shots"');
    expect(source.match(/<ModelPicker config=\{effectiveConfig\} capability="text"/g)).toHaveLength(1);
    expect(source).toContain("const textModel = selectedTextModel");
    expect(source).toContain("generateChapterStoryboard");
    // 提取资产也走服务端步骤（assets.extract），页面不再解析模板输出、不再写待确认候选。
    expect(source).toContain("<AssetsExtractControl");
    expect(source).not.toContain("extractChapterAssets");
    expect(source).not.toContain("createProjectAssetCandidates");
    const control = await Bun.file(new URL("../src/pages/projects/workbench/detail/chapter-assets-extract.tsx", import.meta.url)).text();
    expect(control).toContain('getDramaStepUsage(projectId, "assets.extract"');
    expect(control).toContain("runAssetsExtract(");
});

test("镜头画面使用已绑定资产的 @ 引用编辑器", async () => {
    const source = await Bun.file(new URL("../src/pages/projects/workbench/detail/workflow-production-workbench.tsx", import.meta.url)).text();
    expect(source).toContain('name="plotDescription"');
    expect(source).toContain('<ShotAssetMentionTextarea variant="scene"');
    expect(source).toContain("resolveShotAssetMentionPrompt(basePrompt, shotAssetReferenceContext");
    expect(source).toContain("<Image.PreviewGroup>");
});

test("技能选择器占满表单宽度", async () => {
    const source = await Bun.file(new URL("../src/components/skills/skill-runtime-picker.tsx", import.meta.url)).text();
    expect(source).toContain('style={{ width: "100%" }}');
});

test("画布分镜生成统一经过 Skill Runtime", async () => {
    const source = await Bun.file(new URL("../src/pages/canvas/use-canvas-storyboard.ts", import.meta.url)).text();
    expect(source).toContain('profile: "shortDrama"');
    expect(source).toContain("skillRuntime.prepare");
    expect(source).toContain("...skillExecution.metadata");
    expect(source).not.toContain("getSkillFile");
    expect(source).not.toContain("getSkillBundle");
});
