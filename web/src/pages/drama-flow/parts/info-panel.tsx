import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Radio } from "antd";

import { AppDrawer } from "@/components/ui/product/app-drawer/app-drawer";
import { BRIEF_FACTS, briefEpisodeSeconds, briefFactsFromDocument, briefSegmentSeconds } from "@/pages/projects/workbench/detail/drama-brief";
import { DramaBriefModal, dramaProgressKey } from "@/pages/projects/workbench/detail/drama-panels";
import { DRAMA_RUN_MODES, DRAMA_VIDEO_STOP_NOTE } from "@/pages/projects/workbench/drama-model";
import { updateProject } from "@/services/api/projects";
import { getWallet } from "@/services/api/wallet";
import type { DramaRunMode } from "@/services/api/project-drama";

import { projectTasksKey, type FlowData } from "../hooks/use-flow-data";
import { creditsText, totalCost } from "../model/costs";
import { PaneSection } from "./layout";

const VISUAL_TYPE_LABELS: Record<string, string> = { live_action: "真人实拍", cg_real: "写实 CG", anime_3d: "三维动画", anime_2d: "二维动画" };

/** 项目信息：需求文档六项、生成模式（切到「每步都问我」就是先停在这一步）、时长、模型、花费和余额。 */
export function InfoPanel({ open, data, onClose }: { open: boolean; data: FlowData; onClose: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [briefOpen, setBriefOpen] = useState(false);
    const [savingMode, setSavingMode] = useState(false);
    const { project, brief, briefQuery, pipeline, dramaModel, videoModel, projectTasks } = data;
    const walletQuery = useQuery({ queryKey: ["wallet", "summary"], queryFn: () => getWallet(1, 1), enabled: open });
    if (!project) return null;
    const runMode = (project.runMode || pipeline?.runMode || "until_video") as DramaRunMode;
    const facts = brief ? briefFactsFromDocument(brief) : undefined;
    const episodeSeconds = briefEpisodeSeconds(brief);
    const segmentSeconds = briefSegmentSeconds(brief);

    const changeMode = async (mode: DramaRunMode) => {
        if (mode === runMode) return;
        setSavingMode(true);
        try {
            await updateProject(project.id, { runMode: mode });
            await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", project.id] }), queryClient.invalidateQueries({ queryKey: dramaProgressKey(project.id) }), queryClient.invalidateQueries({ queryKey: projectTasksKey(project.id) })]);
            message.success("生成模式已改：只影响之后的接续，已经停住的环节不会自动补做");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "生成模式没改成");
        } finally {
            setSavingMode(false);
        }
    };

    return (
        <AppDrawer open={open} onClose={onClose} title="项目信息" width={480}>
            <div className="drama-flow-info">
                <PaneSection label="生成模式">
                    <Radio.Group value={runMode} disabled={savingMode} onChange={(event) => void changeMode(event.target.value as DramaRunMode)}>
                        <div className="drama-flow-info-modes">
                            {DRAMA_RUN_MODES.map((mode) => (
                                <Radio key={mode.value} value={mode.value}>
                                    <strong>{mode.label}</strong>
                                    <span>{mode.hint}</span>
                                </Radio>
                            ))}
                        </div>
                    </Radio.Group>
                    <p className="drama-flow-muted">想先停在这一步，就切到「每步都问我」：正在做的这一步做完就停。{DRAMA_VIDEO_STOP_NOTE}</p>
                </PaneSection>
                <PaneSection label="制作参数">
                    <dl className="drama-flow-info-facts">
                        <dt>画面类型</dt>
                        <dd>{VISUAL_TYPE_LABELS[project.visualType || ""] || "没选"}</dd>
                        <dt>画幅</dt>
                        <dd>{project.aspectRatio || "没设"}</dd>
                        <dt>每集时长</dt>
                        <dd>{episodeSeconds ? `${episodeSeconds} 秒` : briefQuery.isError ? "没读到" : "没设"}</dd>
                        <dt>单段时长</dt>
                        <dd>{segmentSeconds ? `${segmentSeconds} 秒` : dramaModel ? `按模型上限 ${dramaModel.duration.max} 秒` : "没读到"}</dd>
                        <dt>生成模型</dt>
                        <dd>{dramaModel?.label || project.generationModel || "没选"}</dd>
                        <dt>视频模型</dt>
                        <dd>{videoModel || "还没配好"}</dd>
                        <dt>图片模型</dt>
                        <dd>{project.defaultImageModel || "用全局默认"}</dd>
                    </dl>
                </PaneSection>
                <PaneSection label="花费">
                    <dl className="drama-flow-info-facts">
                        <dt>最近 100 次运行</dt>
                        <dd>{creditsText(totalCost(projectTasks))}</dd>
                        <dt>账户余额</dt>
                        <dd>{walletQuery.data ? creditsText(walletQuery.data.account.availableMicrocredits) : walletQuery.isError ? "没读到" : "…"}</dd>
                    </dl>
                </PaneSection>
                <PaneSection label="需求文档">
                    {facts ? (
                        <dl className="drama-flow-info-facts">
                            {BRIEF_FACTS.map((fact) => (
                                <div key={fact.key} className="contents">
                                    <dt>{fact.label}</dt>
                                    <dd>
                                        {facts[fact.key]?.value || "没填"}
                                        <em>{facts[fact.key]?.status}</em>
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    ) : (
                        <p className="drama-flow-muted">{briefQuery.isError ? "需求文档没读到。" : "正在读需求文档…"}</p>
                    )}
                    <Button size="small" onClick={() => setBriefOpen(true)}>
                        编辑需求文档
                    </Button>
                </PaneSection>
            </div>
            <DramaBriefModal projectId={project.id} open={briefOpen} onClose={() => setBriefOpen(false)} />
        </AppDrawer>
    );
}
