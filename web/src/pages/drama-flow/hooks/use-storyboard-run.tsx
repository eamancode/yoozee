import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { useDirectorPrecondition } from "@/pages/projects/workbench/detail/director-preconditions";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App } from "antd";

import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { navigateToSettings } from "@/lib/settings-navigation";
import { parseShotStream } from "@/pages/projects/workbench/detail/chapter-shot-stream";
import { projectStyleMissing } from "@/pages/projects/workbench/detail/chapter-style-lock";
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
    const navigate = useNavigate();
    const handlePrecondition = useDirectorPrecondition();
    const queryClient = useQueryClient();
    const effectiveConfig = useDramaModelConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const { project, unitId, dramaRule, allUnits, projectTasks } = data;
    const projectId = project?.id || "";
    const episode = unitEpisodeNumber(allUnits, unitId);
    const code = (text: string) => projectSceneCode(text, episode);
    const [streamText, setStreamText] = useState("");
    const [liveTaskId, setLiveTaskId] = useState("");
    // 确认框可能跨过模型目录更新、换集或离开页面，只使用当前仍挂载的项目和章节。
    const current = useRef<{ data: FlowData; config: typeof effectiveConfig } | null>(null);
    useLayoutEffect(() => {
        current.current = { data, config: effectiveConfig };
        return () => { current.current = null; };
    }, [data, effectiveConfig]);

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

    const sameTarget = () => current.current?.data.project?.id === projectId && current.current.data.unitId === unitId;
    const generationInput = () => {
        const snapshot = current.current;
        if (!snapshot || !sameTarget()) return;
        const { project, unit, detail, textModel, stepModels, segmentRule, dramaRule } = snapshot.data;
        if (!project || !unit || !detail) return;
        // 和旧工作台同一口径：没有锁定的风格锚、画风快照也没写风格提示词，才拦下。
        if (projectStyleMissing(project)) {
            void message.warning("项目还没锁定画风：先到「定妆 · 画风」让导演定画风，再生成分镜");
            return;
        }
        if (!segmentRule || segmentRule.problem) {
            void message.warning(`${segmentRule?.problem || "生成模型没读到"}，先在项目信息里换模型`);
            return;
        }
        if (!textModel || stepModels.text.disabledReason(textModel)) {
            void message.warning(stepModels.text.status("请先选择可用的文本模型").text);
            return;
        }
        const config = { ...snapshot.config, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        return { detail, config, rule: dramaRule };
    };
    const generate = async (onlySections?: string[], independent = false) => {
        let input = generationInput();
        if (!input) return;
        const whole = !onlySections?.length;
        if (whole && !independent) {
            const discards = chapterSegmentDiscards(input.detail.shots, input.detail.shotArtifacts, unitId);
            if (discards.length) {
                if (!(await confirmDiscard(discards, "整集重新生成"))) return;
                input = generationInput();
                if (!input) return;
            }
        }
        setStreamText("");
        try {
            const result = await generateChapterStoryboard(
                { projectId, chapterId: unitId, config: input.config, rule: input.rule, onlySections, discard: whole, allowMissingHandoff: independent },
                {
                    onTaskUpdate: (task) => setLiveTaskId(task.id),
                    onStreamText: (text) => setStreamText(text),
                },
            );
            const outcome = result.outcome;
            if (outcome?.status === "draft") message.warning(`镜头表检查有 ${outcome.errors} 个问题，存成了草稿还没装段；在上面的条里处理`, 8);
            else message.success(outcome ? `已生成 ${outcome.segments} 段（${outcome.scenes} 场）` : "分镜已写入");
        } catch (error) {
            if (!sameTarget()) return;
            if (
                handlePrecondition(error, {
                    previous: (id) => { if (sameTarget()) navigate(`/drama-flow/${projectId}/shots?ep=${encodeURIComponent(id)}`); },
                    independent: () => void generate(onlySections, true),
                    design: () => { if (sameTarget()) navigate(`/drama-flow/${projectId}/script?ep=${encodeURIComponent(unitId)}`); },
                })
            )
                return;
            message.error(error instanceof Error ? `分镜生成失败：${error.message}` : "分镜生成失败");
        } finally {
            setLiveTaskId("");
            await refresh();
        }
    };

    return { shotlist, shotlistQuery, latest, running, stream, streamText, problemSections, repack, generate, code };
}

export type StoryboardRun = ReturnType<typeof useStoryboardRun>;
