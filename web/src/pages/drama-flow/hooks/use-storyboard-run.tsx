import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App } from "antd";

import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { navigateToSettings } from "@/lib/settings-navigation";
import { parseShotStream } from "@/pages/projects/workbench/detail/chapter-shot-stream";
import { projectLockedStyle } from "@/pages/projects/workbench/detail/chapter-style-lock";
import { dramaShotlistKey } from "@/pages/projects/workbench/detail/drama-panels";
import { chapterTaskIdentity, generateChapterStoryboard } from "@/pages/projects/workbench/detail/project-chapter-ai";
import { chapterSegmentDiscards, segmentDiscardLine } from "@/pages/projects/workbench/detail/segment-discard";
import { issueSections } from "@/pages/projects/workbench/detail/shot-issues";
import { projectSceneCode, unitEpisodeNumber } from "@/pages/projects/workbench/detail/unit-numbering";
import { getDramaShotlist, repackDramaShotlist, segmentsDiscardRequired, shotlistCheckFailure, type DramaSegmentDiscard } from "@/services/api/project-drama";
import { useConfigStore } from "@/stores/use-config-store";

import { projectTasksKey, type FlowData } from "./use-flow-data";

const active = (status: string) => status === "queued" || status === "running";

/**
 * 一集的分镜生成与装段：整集重新生成、只重写有问题的小节、重新装段、带着问题先装段。
 * 规则和旧章节页一致：会删掉已有分镜图、预演、视频的操作，列出会删的段确认一次（这是除花费外唯一保留的确认）。
 */
export function useStoryboardRun(data: FlowData) {
    const { message, modal } = App.useApp();
    const queryClient = useQueryClient();
    const effectiveConfig = useDramaModelConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const { project, unit, unitId, detail, dramaRule, segmentRule, allUnits, projectTasks } = data;
    const projectId = project?.id || "";
    const episode = unitEpisodeNumber(allUnits, unitId);
    const code = (text: string) => projectSceneCode(text, episode);
    const [streamText, setStreamText] = useState("");
    const [liveTaskId, setLiveTaskId] = useState("");

    const shotlistQuery = useQuery({ queryKey: dramaShotlistKey(projectId, unitId), queryFn: async ({ signal }) => (await getDramaShotlist(projectId, unitId, signal)).shotlist, enabled: Boolean(projectId && unitId) });
    const shotlist = shotlistQuery.data;
    const tasks = useMemo(() => projectTasks.filter((task) => chapterTaskIdentity(task)?.kind === "storyboard" && chapterTaskIdentity(task)?.chapterId === unitId), [projectTasks, unitId]);
    const latest = tasks[0];
    const running = Boolean(liveTaskId) || Boolean(latest && active(latest.status));
    const stream = useMemo(() => parseShotStream(streamText), [streamText]);
    const problemSections = shotlist?.draft && shotlist.sections?.length ? issueSections(shotlist.issues) : [];

    const refresh = () =>
        Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: projectTasksKey(projectId) }), queryClient.invalidateQueries({ queryKey: ["project", projectId, "drama-shotlist"] })]);

    const discardList = (discards: DramaSegmentDiscard[]) => (
        <ul className="drama-flow-pane-list" aria-label="会删掉的产物">
            {discards.map((item) => (
                <li key={item.shotId}>{segmentDiscardLine(item, code)}</li>
            ))}
        </ul>
    );
    const confirmDiscard = (discards: DramaSegmentDiscard[], action: string) =>
        new Promise<boolean>((resolve) => {
            modal.confirm({
                title: `${action}会删掉 ${discards.length} 段已有的产物`,
                content: (
                    <div className="drama-flow-confirm">
                        <p>这些段在新的分段里对不上，连同下面的分镜图、预演、视频一起删掉，删了找不回来；其余段的产物都保留（内容变了的标「出片后改过」）。</p>
                        {discardList(discards)}
                    </div>
                ),
                okText: `删掉并${action}`,
                okButtonProps: { danger: true },
                cancelText: "先不做",
                centered: true,
                onOk: () => resolve(true),
                onCancel: () => resolve(false),
            });
        });

    const repack = useMutation({
        mutationFn: ({ accept, discard }: { accept?: boolean; discard?: boolean }) => repackDramaShotlist(projectId, unitId, { rule: dramaRule, ...(accept ? { acceptIssues: true } : {}), ...(discard ? { discard: true } : {}) }),
        onSuccess: async (result, { accept }) => {
            await refresh();
            message.success(accept && result.accepted ? `已带着 ${result.check.errors} 个问题装成 ${result.segments.length} 段，问题标在对应的段上` : `已装成 ${result.segments.length} 段`);
        },
        onError: (error, variables) => {
            const discards = segmentsDiscardRequired(error);
            if (discards) {
                void confirmDiscard(discards, variables.accept ? "先装段" : "重新装段").then((ok) => ok && repack.mutate({ ...variables, discard: true }));
                return;
            }
            const check = shotlistCheckFailure(error);
            message.error(check ? `装段仍有 ${check.errors} 个问题，现有的段没变；只重写有问题的小节或整集重新生成` : error instanceof Error ? error.message : "装段没成功");
            void refresh();
        },
    });

    const generate = async (onlySections?: string[]) => {
        if (!project || !unit || !detail) return;
        if (!projectLockedStyle(project.bibleJson)) return message.warning("项目还没锁定画风：先到「定妆 · 画风」让导演定画风，再生成分镜");
        if (!segmentRule || segmentRule.problem) return message.warning(`${segmentRule?.problem || "生成模型没读到"}，先在项目信息里换模型`);
        const textModel = effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!textModel || !isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        const whole = !onlySections?.length;
        if (whole) {
            const discards = chapterSegmentDiscards(detail.shots, detail.shotArtifacts, unit.id);
            if (discards.length && !(await confirmDiscard(discards, "整集重新生成"))) return;
        }
        setStreamText("");
        try {
            const result = await generateChapterStoryboard(
                { projectId, chapterId: unit.id, config, rule: dramaRule, onlySections, discard: whole },
                {
                    onTaskUpdate: (task) => setLiveTaskId(task.id),
                    onStreamText: (text) => setStreamText(text),
                },
            );
            const outcome = result.outcome;
            if (outcome?.status === "draft") message.warning(`镜头表检查有 ${outcome.errors} 个问题，存成了草稿还没装段；在上面的条里处理`, 8);
            else message.success(outcome ? `已生成 ${outcome.segments} 段（${outcome.scenes} 场）` : "分镜已写入");
        } catch (error) {
            message.error(error instanceof Error ? `分镜生成失败：${error.message}` : "分镜生成失败");
        } finally {
            setLiveTaskId("");
            await refresh();
        }
    };

    return { shotlist, shotlistQuery, latest, running, stream, streamText, problemSections, repack, generate, code };
}

export type StoryboardRun = ReturnType<typeof useStoryboardRun>;
