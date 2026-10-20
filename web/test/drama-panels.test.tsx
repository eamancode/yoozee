import { describe, expect, test } from "bun:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { segmentDurationRule } from "@/pages/projects/detail/chapter-segments";
import { DramaAssetChecklist, DramaDeliveryPanel, DramaModelChips, DramaOverviewPanel, DramaSegmentCard, DramaTakeCompare, DramaTakeEvidencePanel, dramaBibleKey, dramaChecklistAssetsKey, dramaEvidenceKey, dramaProgressKey, dramaSegmentsKey, takeEvidenceStatus } from "@/pages/projects/detail/drama-panels";
import type { DramaBible, DramaEvidence, DramaProgress, DramaSegment, DramaSegmentProgress, DramaSegmentView } from "@/services/api/drama";
import type { ProjectAsset, ShotArtifact } from "@/services/api/projects";

const model = {
    id: "seedance-2.0",
    label: "Seedance 2.0",
    vendor: "ByteDance",
    kind: "video",
    duration: { min: 4, max: 15, step: 1 },
    refs: { images: 9, videos: 3, audios: 3, total: null },
    frames_exclusive_with_refs: true,
    prompt_max_chars: 10000,
    resolutions: ["480p"],
    calibrated: true,
    checked: "2026-10-15",
};

function segment(patch: Partial<DramaSegmentProgress> = {}): DramaSegmentProgress {
    return {
        shotId: "shot-1",
        unitId: "unit-1",
        unitTitle: "第一章",
        segmentId: "S01",
        title: "七床",
        durationMs: 8000,
        overLimit: false,
        hasDialogue: true,
        script: true,
        refs: false,
        generated: false,
        reviewed: false,
        inCut: false,
        accepted: false,
        takes: 0,
        ...patch,
    };
}

function progress(patch: Partial<DramaProgress> = {}): DramaProgress {
    const segments = patch.segments ?? [segment()];
    return {
        model,
        authMode: "step",
        segments,
        counts: { segments: segments.length, script: 0, refs: 0, generated: 0, takes: 0, reviewed: 0, inCut: 0, accepted: 0, overLimit: 0, ...(patch.counts ?? {}) },
        line: "1 段 · 剧本 1",
        endStates: [],
        ...patch,
    };
}

const endState = (key: string, done: boolean) => ({ key, label: key, done, detail: `${key} 的说明`, skill: `yz-${key}` });

function evidence(patch: Partial<DramaEvidence> = {}): DramaEvidence {
    return { id: "e1", projectId: "p1", kind: "gate", passed: true, metricsJson: "{}", note: "", createdAt: "2026-10-15T10:00:00Z", ...patch };
}

const artifact = { id: "take-1", projectId: "p1", shotId: "shot-1", type: "video", version: 3, status: "ready", selected: false, metadataJson: "{}", createdAt: "", updatedAt: "" } as ShotArtifact;

function render(element: ReactElement, seed: { progress?: DramaProgress; evidence?: DramaEvidence[]; bible?: DramaBible; assets?: ProjectAsset[]; segments?: DramaSegmentView[] } = {}) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    if (seed.progress) client.setQueryData(dramaProgressKey("p1"), seed.progress);
    if (seed.evidence) client.setQueryData(dramaEvidenceKey("p1"), seed.evidence);
    if (seed.bible) client.setQueryData(dramaBibleKey("p1"), seed.bible);
    if (seed.assets) client.setQueryData(dramaChecklistAssetsKey("p1"), seed.assets);
    if (seed.segments) client.setQueryData(dramaSegmentsKey("p1", "unit-1"), seed.segments);
    return renderToStaticMarkup(
        <QueryClientProvider client={client}>
            <App>{element}</App>
        </QueryClientProvider>,
    );
}

describe("概览：模型与档位标签", () => {
    test("显示每段上限、授权档位和未校准提示", () => {
        const html = render(<DramaModelChips progress={progress()} />);
        expect(html).toContain("Seedance 2.0 · 每段最长 15 秒");
        expect(html).toContain("每步确认");
        expect(html).not.toContain("档案未校准");
        expect(html).not.toContain("段超长");
    });

    test("全权档位、未校准档案与超长段数都如实标出", () => {
        const html = render(<DramaModelChips progress={progress({ authMode: "full", model: { ...model, calibrated: false }, counts: { segments: 3, script: 3, refs: 0, generated: 0, takes: 0, reviewed: 0, inCut: 0, accepted: 0, overLimit: 2 } })} />);
        expect(html).toContain("全权");
        expect(html).toContain("档案未校准");
        expect(html).toContain("2 段超长");
    });
});

describe("概览：一句进度、八个终态与每段六格", () => {
    test("空项目显示进度行与下一步", () => {
        const html = render(<DramaOverviewPanel projectId="p1" />, {
            progress: progress({ line: "还没有分段", endStates: [endState("brief", false), endState("bible", false)], next: { key: "brief", label: "问需求", detail: "先问清用途和平台", skill: "yz-brief" } }),
        });
        expect(html).toContain("还没有分段");
        expect(html).toContain("下一步：问需求 · 先问清用途和平台（yz-brief）");
    });

    test("八个终态全达成时不再给下一步", () => {
        const states = Array.from({ length: 8 }, (_, index) => endState(`s${index}`, true));
        const html = render(<DramaOverviewPanel projectId="p1" />, { progress: progress({ endStates: states }) });
        expect(html).toContain("8 个终态全部达成。");
        expect(html).not.toContain("下一步：");
    });

    test("超长的段标出「超长」，缺证据的段列出缺口", () => {
        const html = render(<DramaOverviewPanel projectId="p1" />, {
            progress: progress({
                segments: [segment({ shotId: "shot-1", segmentId: "S01", durationMs: 8000 }), segment({ shotId: "shot-2", segmentId: "S02", durationMs: 20000, overLimit: true, script: false, takes: 2, missingEvidence: ["同一个人", "台词一字不差"] })],
                counts: { segments: 2, script: 1, refs: 0, generated: 0, takes: 2, reviewed: 0, inCut: 0, accepted: 0, overLimit: 1 },
            }),
        });
        expect(html).toContain("20.0s 超长");
        expect(html).toContain("8.0s");
        expect(html).toContain("2 条 · 缺同一个人、台词一字不差");
        expect(html).toContain("1 段超长");
    });

    test("六格表头齐全（剧本/参考/生成/过审/入片/验收）", () => {
        const html = render(<DramaOverviewPanel projectId="p1" />, { progress: progress() });
        for (const label of ["剧本", "参考", "生成", "过审", "入片", "验收"]) expect(html).toContain(`>${label}<`);
    });
});

describe("候选证据面板：三项都过才能选定", () => {
    test("还没做任何证据时列出全部缺口，选定按钮禁用", () => {
        const html = render(<DramaTakeEvidencePanel projectId="p1" shotId="shot-1" artifact={artifact} characterImages={[]} onPicked={() => undefined} />, {
            progress: progress({ segments: [segment({ hasDialogue: true })] }),
            evidence: [],
        });
        expect(html).toContain("还缺：抽帧、人脸、台词（三项都过才能选定）");
        expect(html).toContain("候选证据 · v3");
        expect(html).toContain("disabled");
    });

    test("人判的抽帧与人脸过了以后只剩理由这道关", () => {
        const html = render(<DramaTakeEvidencePanel projectId="p1" shotId="shot-1" artifact={artifact} characterImages={[]} onPicked={() => undefined} />, {
            progress: progress({ segments: [segment({ hasDialogue: false })] }),
            evidence: [
                evidence({ id: "e-frames", artifactId: "take-1", kind: "frames", passed: true, resourceId: "res-1", createdAt: "2026-10-15T10:00:00Z" }),
                evidence({ id: "e-faces", artifactId: "take-1", kind: "faces", passed: true, createdAt: "2026-10-15T10:01:00Z" }),
            ],
        });
        expect(html).not.toContain("还缺：");
        // 没台词就不要求转写，因此不出现台词这一行。
        expect(html).not.toContain("转写并核对");
        expect(html).toContain("通过");
        expect(html).toContain("同一个人");
        // 三项都过、但还没写理由，所以选定仍然禁用。
        expect(html).toMatch(/ant-btn-primary[^>]*disabled=""/);
    });

    test("有人判了「不是同一个人」时仍算缺，不允许选定", () => {
        const html = render(<DramaTakeEvidencePanel projectId="p1" shotId="shot-1" artifact={artifact} characterImages={[]} onPicked={() => undefined} />, {
            progress: progress({ segments: [segment({ hasDialogue: false })] }),
            evidence: [
                evidence({ id: "e-frames", artifactId: "take-1", kind: "frames", passed: true, resourceId: "res-1", createdAt: "2026-10-15T10:00:00Z" }),
                evidence({ id: "e-faces", artifactId: "take-1", kind: "faces", passed: false, createdAt: "2026-10-15T10:01:00Z" }),
            ],
        });
        expect(html).toContain("还缺：人脸（三项都过才能选定）");
        expect(html).toContain("未通过");
    });

    test("候选不是 ready 状态时不给选定", () => {
        const html = render(<DramaTakeEvidencePanel projectId="p1" shotId="shot-1" artifact={{ ...artifact, status: "running" }} characterImages={[]} onPicked={() => undefined} />, {
            progress: progress({ segments: [segment({ hasDialogue: false })] }),
            evidence: [
                evidence({ id: "e-frames", artifactId: "take-1", kind: "frames", passed: true, resourceId: "res-1", createdAt: "2026-10-15T10:00:00Z" }),
                evidence({ id: "e-faces", artifactId: "take-1", kind: "faces", passed: true, createdAt: "2026-10-15T10:01:00Z" }),
            ],
        });
        expect(html).toContain("disabled");
    });
});

describe("交付面板：门禁明细与交付包", () => {
    test("没有门禁记录时只给检查入口", () => {
        const html = render(<DramaDeliveryPanel projectId="p1" projectName="夜班" />, { progress: progress(), evidence: [] });
        expect(html).toContain("检查成片");
        expect(html).toContain("下载交付包");
        expect(html).not.toContain("最近一次门禁");
    });

    test("有门禁记录时展示逐项结果", () => {
        const html = render(<DramaDeliveryPanel projectId="p1" projectName="夜班" />, {
            progress: progress(),
            evidence: [
                evidence({
                    kind: "gate",
                    passed: false,
                    metricsJson: JSON.stringify({
                        checks: [
                            { name: "音画同步", ok: true, detail: "等长" },
                            { name: "响度", ok: false, detail: "差 3 LUFS" },
                        ],
                    }),
                }),
            ],
        });
        expect(html).toContain("最近一次门禁未通过");
        expect(html).toContain("音画同步：等长");
        expect(html).toContain("响度：差 3 LUFS");
        expect(html).toContain("重新检查成片");
    });

    test("门禁指标损坏时只显示结论，不崩", () => {
        const html = render(<DramaDeliveryPanel projectId="p1" projectName="夜班" />, {
            progress: progress(),
            evidence: [evidence({ kind: "gate", passed: true, metricsJson: "{不是 JSON" })],
        });
        expect(html).toContain("最近一次门禁通过");
    });
});

describe("角色与资产：素材清单", () => {
    const lockCard = ["齐耳短发", "左眉一颗痣", "护士服左胸别两支笔", "圆框眼镜", "工牌挂绳蓝色", "白色护士鞋", "右手腕黑色皮筋", "口罩挂在下巴"];

    test("连戏表为空时给起草入口", () => {
        const html = render(<DramaAssetChecklist projectId="p1" />, { bible: {}, assets: [{ id: "a1", title: "林小满", category: "character", mediaType: "image", status: "ready", versionCount: 1, usages: [], position: 0, updatedAt: "" } as ProjectAsset] });
        expect(html).toContain("按已确认资产起草清单");
    });

    test("列出没过门禁的项和缺口，文件不齐的不能勾", () => {
        const bible: DramaBible = {
            characters: [{ name: "林小满", variants: [{ label: "默认", sheet: "v-lin", lock_card: lockCard, voice: "voice-lin", checked: true }] }],
            locations: [{ name: "七床病房" }],
        };
        const html = render(<DramaAssetChecklist projectId="p1" />, { bible, assets: [] });
        expect(html).toContain("过门禁 1/2");
        expect(html).toContain("还有 1 项没过门禁");
        expect(html).toContain("七床病房");
        expect(html).toContain("还没有无人空景图");
        expect(html).not.toContain("林小满");
    });
});

describe("候选并排对比", () => {
    const take = (id: string, version: number, selected = false) => ({ ...artifact, id, version, selected, resourceId: `res-${id}` }) as ShotArtifact;

    test("不到两条就绪候选时不出现入口", () => {
        const html = render(<DramaTakeCompare projectId="p1" shotId="shot-1" artifacts={[take("t1", 1), { ...take("t2", 2), status: "running" }]} onOpen={() => undefined} />, { progress: progress() });
        expect(html).not.toContain("并排对比");
    });

    test("两条以上就绪候选时给并排入口", () => {
        const html = render(<DramaTakeCompare projectId="p1" shotId="shot-1" artifacts={[take("t1", 1), take("t2", 2, true)]} onOpen={() => undefined} />, { progress: progress() });
        expect(html).toContain("并排对比 2 条候选");
    });

    test("证据现状：有台词要三项，取最新一条，带字错率", () => {
        const status = takeEvidenceStatus([
            evidence({ id: "f1", artifactId: "t1", kind: "frames", passed: false, createdAt: "2026-10-15T10:00:00Z" }),
            evidence({ id: "f2", artifactId: "t1", kind: "frames", passed: true, createdAt: "2026-10-15T11:00:00Z" }),
            evidence({ id: "tr", artifactId: "t1", kind: "transcript", passed: true, metricsJson: JSON.stringify({ cer: 0.02 }) }),
            evidence({ id: "other", artifactId: "t2", kind: "faces", passed: true }),
        ], "t1", true);
        expect(status.required.map((item) => [item.kind, item.passed])).toEqual([["frames", true], ["faces", null], ["transcript", true]]);
        expect(status.passedAll).toBe(false);
        expect(status.cer).toBe(0.02);
    });
});

describe("分镜制作页的段卡：段内镜头可拆合、可编辑", () => {
    const spec = (id: string, shots: number): DramaSegment => ({ id, scene: "七床病房", dur: shots * 4, mode: "text_only", shots: Array.from({ length: shots }, (_, index) => ({ at: index * 4, text: `${id} 镜头${index + 1}` })) });
    const view = (id: string, shots: number, derived = false): DramaSegmentView => ({ shotId: `shot-${id}`, unitId: "unit-1", title: id, position: 0, durationMs: shots * 4000, spec: spec(id, shots), derived });
    const card = (rule = segmentDurationRule(30)) => <DramaSegmentCard projectId="p1" unitId="unit-1" shotId="shot-S02" specJson={JSON.stringify(spec("S02", 3))} durationRule={rule} />;

    test("有相邻段时给合并入口，每个镜头有铅笔和拆合菜单", () => {
        const html = render(card(), { segments: [view("S01", 2), view("S02", 3), view("S03", 1)] });
        expect(html).toContain("并入 S01");
        expect(html).toContain("合并 S03");
        expect(html).toContain("编辑第 1 个镜头的画面、台词和时长");
        expect(html).toContain("第 2 个镜头：拆段或移到相邻段");
        expect(html).toContain("段长按当前视频渠道（最长 30 秒）取档");
    });

    test("段没有参考时提示人物一致性只靠文字，并给「选择资产」入口；有参考时不提示", () => {
        const html = render(<DramaSegmentCard projectId="p1" unitId="unit-1" shotId="shot-S02" specJson={JSON.stringify(spec("S02", 2))} durationRule={segmentDurationRule(30)} onPickAssets={() => undefined} />, { segments: [view("S02", 2)] });
        expect(html).toContain("此段没有人物/场景参考，人物一致性只靠文字。");
        expect(html).toContain("选择资产");
        const withRefs = { ...spec("S02", 2), mode: "lock_identity" as const, refs: [{ file: "version-a", role: "identity", note: "林小满" }] };
        const referenced = render(<DramaSegmentCard projectId="p1" unitId="unit-1" shotId="shot-S02" specJson={JSON.stringify(withRefs)} durationRule={segmentDurationRule(30)} onPickAssets={() => undefined} />, { segments: [view("S02", 2)] });
        expect(referenced).not.toContain("人物一致性只靠文字");
    });

    test("相邻的是旧镜头时不给合并；没有段长规则时只能看", () => {
        const html = render(card(), { segments: [view("S01", 2, true), view("S02", 3)] });
        expect(html).not.toContain("并入 S01");
        const readOnly = render(<DramaSegmentCard projectId="p1" unitId="unit-1" shotId="shot-S02" specJson={JSON.stringify(spec("S02", 3))} />, { segments: [view("S01", 2), view("S02", 3)] });
        expect(readOnly).not.toContain("编辑第 1 个镜头");
        expect(readOnly).toContain("S02 镜头3");
    });
});
