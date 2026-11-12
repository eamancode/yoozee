import { AlertCircle, Check } from "lucide-react";

import type { FlowStageKey, FlowStageView } from "../model/stages";

const STATE_TEXT: Record<FlowStageView["state"], string> = { done: "已完成", running: "正在做", blocked: "等你处理", ready: "可以开始", pending: "还没到" };

/** 五步进度条：既是进度也是导航，点哪一步就看哪一步；不锁步骤，没做到的步骤里会说明还差什么。 */
export function StageStrip({ stages, facts, active, onSelect }: { stages: FlowStageView[]; facts: Record<FlowStageKey, string>; active: FlowStageKey; onSelect: (stage: FlowStageKey) => void }) {
    return (
        <nav className="drama-flow-strip" aria-label="制作步骤">
            {stages.map((stage) => (
                <button key={stage.key} type="button" className="drama-flow-step" data-state={stage.state} aria-current={stage.key === active ? "step" : undefined} onClick={() => onSelect(stage.key)}>
                    <span className="drama-flow-step-mark" aria-hidden="true">
                        {stage.state === "done" ? <Check className="size-3.5" /> : stage.state === "running" ? <span className="drama-flow-spin" /> : stage.state === "blocked" ? <AlertCircle className="size-3.5" /> : stage.index + 1}
                    </span>
                    <span className="drama-flow-step-text">
                        <strong>{stage.label}</strong>
                        <span>{stage.detail || STATE_TEXT[stage.state]}</span>
                        {facts[stage.key] ? <em>{facts[stage.key]}</em> : null}
                    </span>
                </button>
            ))}
        </nav>
    );
}
