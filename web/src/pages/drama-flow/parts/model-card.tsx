import { Pencil } from "lucide-react";

import { ModelPicker } from "@/components/model-picker";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { navigateToSettings } from "@/lib/settings-navigation";
import { useDramaModelPreferencesStore, type DramaModelCapability } from "@/stores/use-drama-model-preferences-store";

import type { StepModel } from "../hooks/use-flow-data";
import type { SummaryItem } from "./layout";

const LABELS: Record<DramaModelCapability, string> = { text: "文本模型", image: "图片模型", video: "视频模型" };
const UNSET = "还没配好，点这里选";

/**
 * 步骤页汇总行里的模型卡。标签、值和留白都在同一个原生按钮内；
 * 卡上写的是状态（目录在读、没读到、已下架、改用了谁），不是下拉自己算的名字。
 * 选中即记成「上次用的」，之后这一步的提交、确认面板默认值都按它走；已在跑的任务不受影响。
 */
function StageModelPicker({ step, text }: { step: StepModel; text: string }) {
    const config = useDramaModelConfig();
    const remember = useDramaModelPreferencesStore((state) => state.remember);
    const label = LABELS[step.capability];
    return (
        <ModelPicker
            preferenceScope="short-drama"
            config={config}
            capability={step.capability}
            value={step.effective}
            onChange={(model) => remember(step.capability, model)}
            modelDisabledReason={step.disabledReason}
            variant="creation"
            fullWidth
            className="drama-flow-model-pick"
            placeholder={UNSET}
            triggerAriaLabel={`更换${label}，当前：${text}`}
            triggerContent={
                <>
                    <span className="drama-flow-model-label">{label}</span>
                    <span className="drama-flow-model-value">
                        <span className="drama-flow-model-name">{text}</span>
                        <Pencil className="drama-flow-model-edit" aria-hidden="true" />
                    </span>
                </>
            }
            showPreferenceNotice={false}
            showSelectedPrice={false}
            showOptionPrices
            onMissingConfig={() => navigateToSettings({ continueCreation: true })}
        />
    );
}

/** 汇总行的一项：模型卡。确认没有可用模型时标红（目录还没读到时不标）；tone 是调用方补充的提醒（如视频跨族），状态本身的警示优先。 */
export function modelSummaryItem(step: StepModel, tone?: SummaryItem["tone"]): SummaryItem {
    const status = step.status(UNSET);
    return {
        label: LABELS[step.capability],
        value: <StageModelPicker step={step} text={status.text} />,
        tone: status.tone || (!step.effective ? (step.catalogRead === "ready" || !step.chosen ? "bad" : undefined) : tone),
        model: true,
    };
}
