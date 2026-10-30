import { Check, Hand, Loader2 } from "lucide-react";

import type { DramaPipelineStage, DramaPipelineStageKey } from "@/services/api/drama";

import { DRAMA_PIPELINE_STAGES } from "./drama-model";
import "./project-create.css";

type StripStage = { key: DramaPipelineStageKey; label: string; state?: DramaPipelineStage["state"]; detail?: string; stop?: boolean };

/**
 * 建项后的流水线：拆剧本 → 导演定画风 → 场次色卡 → 角色场景资产 → 分镜 → 装段 → 出视频 → 剪辑。
 * 建项页按所选生成模式标出停靠点（stops）；项目页按真实进度标出每一格的状态（stages）。
 */
export function PipelineStrip({ stages, stops, className = "" }: { stages?: DramaPipelineStage[]; stops?: DramaPipelineStageKey[]; className?: string }) {
    const items: StripStage[] = stages?.length ? stages : DRAMA_PIPELINE_STAGES.map((stage) => ({ ...stage, stop: stops?.includes(stage.key) }));
    return (
        <ol className={`project-pipeline-strip ${className}`} aria-label="制作流水线">
            {items.map((stage, index) => (
                <li key={stage.key} className={`project-pipeline-strip-item${stage.state ? ` is-${stage.state}` : ""}${stage.stop ? " is-stop" : ""}`} aria-current={stage.state === "stopped" || stage.state === "running" ? "step" : undefined}>
                    {index > 0 ? <span className="project-pipeline-strip-link" aria-hidden /> : null}
                    <span className="project-pipeline-strip-dot" aria-hidden>
                        {stage.state === "done" ? <Check className="size-3" strokeWidth={3} /> : stage.state === "running" ? <Loader2 className="project-pipeline-strip-spin size-3" /> : stage.stop || stage.state === "stopped" ? <Hand className="size-3" /> : null}
                    </span>
                    <span className="project-pipeline-strip-label">{stage.label}</span>
                    {stage.stop ? <span className="sr-only">（在这里停下等你点）</span> : null}
                    {stage.detail ? <span className="project-pipeline-strip-detail">{stage.detail}</span> : null}
                </li>
            ))}
        </ol>
    );
}
