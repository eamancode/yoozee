import { useLayoutEffect, useRef } from "react";
import { useDramaModelPreferencesStore } from "@/stores/use-drama-model-preferences-store";
import { logicalModelIDForConfig, useConfigStore, type AiConfig } from "@/stores/use-config-store";
import type { DramaImageTaskTemplate } from "@/services/api/project-drama";

type DialogModel = { effective: string; disabledReason: (model: string) => string };
export type StepDialogModels = Record<"text" | "image", DialogModel>;

/** 确认框、参考图上传和出图模板准备都可能跨一次目录刷新，只在发送前读取最后已显示的选择。 */
export function useDialogSubmissionModels(steps: StepDialogModels | undefined, config: AiConfig) {
    const latest = useRef<{ steps?: StepDialogModels; config: AiConfig } | null>(null);
    useLayoutEffect(() => {
        latest.current = { steps, config };
        return () => {
            latest.current = null;
        };
    }, [steps, config]);
    const validateImage = (image?: DramaImageTaskTemplate) => {
        const current = latest.current;
        if (!current) throw new Error("已离开当前步骤，请重新打开。");
        if (!image || !current.steps) return;
        const selected = current.steps.image;
        if (image.model !== selected.effective || selected.disabledReason(image.model) || (image.logicalModelId || "") !== (logicalModelIDForConfig({ ...current.config, model: image.model }) || "")) {
            throw new Error("图片模型已变更，请核对当前模型后重新提交。");
        }
    };
    return {
        validateImage,
        textConfig(prepared: AiConfig, resumeModel?: string, image?: DramaImageTaskTemplate) {
            validateImage(image);
            const current = latest.current!;
            if (!current.steps) return prepared;
            const { model, issue } = dialogTextSubmission(current.steps.text, current.config, "", resumeModel);
            if (issue) throw new Error(issue);
            const selected = { ...current.config, model, textModel: model, imageModel: current.steps.image.effective };
            if (!useConfigStore.getState().isAiConfigReady(selected, model)) throw new Error("请先选择可用的文本模型");
            return selected;
        },
    };
}

/** 新任务沿用卡片；续跑复核检查点的原模型。没有步骤卡的旧工作台继续按原默认值提交。 */
export function dialogTextSubmission(step: DialogModel | undefined, config: AiConfig, localValue: string, resumeModel?: string) {
    if (!step) return { model: localValue || config.textModel || config.model || config.textModels[0] || "", issue: "" };
    const model = resumeModel ?? step.effective;
    return { model, issue: step.disabledReason(model) || (model ? "" : "请先选择可用的文本模型") };
}

/** 新入口的弹窗直接使用步骤卡的选择；旧工作台仍在弹窗内单独选模型。 */
export function dialogModelPicker(kind: keyof StepDialogModels, stepModels: StepDialogModels | undefined, localValue: string, setLocalValue: (value: string) => void) {
    const step = stepModels?.[kind];
    return {
        value: step?.effective ?? localValue,
        modelDisabledReason: step?.disabledReason,
        onChange: (value: string) => {
            if (step) useDramaModelPreferencesStore.getState().remember(kind, value);
            setLocalValue(value);
        },
    };
}
