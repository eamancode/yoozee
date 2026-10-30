import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { ArrowRight, Play } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";

import { getDramaProgress, type DramaRunMode } from "@/services/api/drama";
import { updateProject, type Project } from "@/services/api/projects";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import { navigateToSettings } from "@/lib/settings-navigation";

import { ChoiceGroup } from "../create/composer-sections";
import { DRAMA_RUN_MODES, DRAMA_VIDEO_STOP_NOTE } from "../drama-model";
import { PipelineStrip } from "../pipeline-strip";
import { dramaProgressKey } from "./drama-panels";
import { continueProjectPipeline } from "./project-chapter-ai";

/**
 * 项目首屏的流水线：拆剧本 → 导演定画风 → 场次色卡 → 角色场景资产 → 分镜 → 装段 → 出视频 → 剪辑，写明停在哪、等你做什么。
 * 停下时点「继续」由服务端从这一格接着做，之后按生成模式接续；出视频要到制作台带着预估花费点。生成模式可以随时切，
 * 只影响之后的接续，已经停住的环节不追补。
 */
export function ProjectPipelinePanel({ project, firstUnitId, onProjectChange }: { project: Project; firstUnitId?: string; onProjectChange: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const [continuing, setContinuing] = useState(false);
    const [savingMode, setSavingMode] = useState(false);
    const query = useQuery({
        queryKey: dramaProgressKey(project.id),
        queryFn: async ({ signal }) => (await getDramaProgress(project.id, signal)).progress,
        // 有环节在跑时 8 秒刷一次；停在能继续的环节时慢刷（出图任务落库后才接分镜，中间有一小段看着像停了），
        // 停在出视频、剪辑或要用户自己处理时不刷。
        refetchInterval: (current) => {
            const pipeline = current.state.data?.pipeline;
            if (!pipeline) return false;
            if (!pipeline.stop) return pipeline.current ? 8000 : false;
            return pipeline.stop.continuable ? 20000 : false;
        },
    });
    const pipeline = query.data?.pipeline;
    const stop = pipeline?.stop;
    const running = pipeline && !stop && pipeline.current ? pipeline.stages.find((stage) => stage.key === pipeline.current) : undefined;
    const runMode = (project.runMode || pipeline?.runMode || "until_video") as DramaRunMode;
    const projectRoot = `/projects/${project.id}`;

    const refresh = () => Promise.all([queryClient.invalidateQueries({ queryKey: dramaProgressKey(project.id) }), queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", project.id] })]);

    const resume = async () => {
        const textModel = effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!textModel || !isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        setContinuing(true);
        try {
            const result = await continueProjectPipeline(project.id, config, project.defaultImageModel);
            message.success(result.images ? `开始给 ${result.images.queued.length + result.images.waiting.length} 个资产出图` : `开始：${result.pipeline.stages.find((stage) => stage.key === result.stage)?.label || "下一步"}`);
            await refresh();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "没能继续");
        } finally {
            setContinuing(false);
        }
    };

    const changeMode = async (mode: DramaRunMode) => {
        if (mode === runMode) return;
        setSavingMode(true);
        try {
            await updateProject(project.id, { runMode: mode });
            onProjectChange();
            await refresh();
            message.success("生成模式已改：只影响之后的接续，已经停住的环节不会自动补做");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "生成模式没改成");
        } finally {
            setSavingMode(false);
        }
    };

    return (
        <section className="project-pipeline-panel" aria-label="制作流水线">
            <div className="project-pipeline-panel-head">
                <div className="min-w-0">
                    <h2>制作流水线</h2>
                    {query.isError ? <p>流水线读取失败：{query.error instanceof Error ? query.error.message : "请稍后重试"}</p> : stop ? (
                        <p className="project-pipeline-panel-stop" role="status">
                            停在{stop.label}：{stop.reason}
                        </p>
                    ) : running ? (
                        <p role="status">正在{running.label}…</p>
                    ) : pipeline && !pipeline.current ? (
                        <p>全部做完了</p>
                    ) : (
                        <p>正在读取进度…</p>
                    )}
                </div>
                <div className="project-pipeline-panel-actions">
                    {stop?.continuable ? (
                        <Button type="primary" icon={<Play className="size-3.5" />} loading={continuing} onClick={() => void resume()}>
                            {stop.action}
                        </Button>
                    ) : stop?.stage === "video" ? (
                        <Link className="project-pipeline-panel-link" to={firstUnitId ? `${projectRoot}/workflow/${firstUnitId}/video` : `${projectRoot}/chapters`}>
                            {stop.action}
                            <ArrowRight className="size-3.5" />
                        </Link>
                    ) : stop?.stage === "cut" ? (
                        <Link className="project-pipeline-panel-link" to={firstUnitId ? `${projectRoot}/workflow/${firstUnitId}/delivery` : `${projectRoot}/chapters`}>
                            {stop.action}
                            <ArrowRight className="size-3.5" />
                        </Link>
                    ) : stop ? (
                        <Link className="project-pipeline-panel-link" to={`${projectRoot}/chapters`}>
                            {stop.action}
                            <ArrowRight className="size-3.5" />
                        </Link>
                    ) : null}
                </div>
            </div>
            <PipelineStrip stages={pipeline?.stages} stops={pipeline ? undefined : DRAMA_RUN_MODES.find((item) => item.value === runMode)?.stops} />
            <div className="project-pipeline-panel-mode">
                <span className="project-create-field-label">生成模式</span>
                <ChoiceGroup
                    label="生成模式"
                    value={runMode}
                    options={DRAMA_RUN_MODES.map((item) => ({ value: item.value, disabled: savingMode }))}
                    onChange={(mode) => void changeMode(mode)}
                    className="project-segmented"
                    optionClassName="project-segmented-item"
                    render={(value) => DRAMA_RUN_MODES.find((item) => item.value === value)?.label}
                />
                <span className="project-create-note">{DRAMA_VIDEO_STOP_NOTE}</span>
            </div>
        </section>
    );
}
