import { Button } from "antd";

import type { FlowAction, FlowPrimary } from "../model/primary-action";

/** 底部操作条：一句话说现在什么情况，最多一个主按钮加一个次按钮；正在自动做的时候没有主按钮。 */
export function ActionBar({ flow, busy, onAction }: { flow: FlowPrimary; busy?: FlowAction["kind"]; onAction: (action: FlowAction) => void }) {
    return (
        <footer className="drama-flow-bar">
            <div className="drama-flow-bar-status" role="status" aria-live="polite">
                <span className="drama-flow-dot" data-tone={flow.tone} aria-hidden="true" />
                <span>{flow.status}</span>
            </div>
            <div className="drama-flow-bar-actions">
                {flow.secondary ? (
                    <Button loading={busy === flow.secondary.kind} disabled={Boolean(busy) && busy !== flow.secondary.kind} onClick={() => onAction(flow.secondary!)}>
                        {flow.secondary.label}
                    </Button>
                ) : null}
                {flow.primary ? (
                    <Button type="primary" loading={busy === flow.primary.kind} disabled={Boolean(busy) && busy !== flow.primary.kind} onClick={() => onAction(flow.primary!)}>
                        {flow.primary.label}
                    </Button>
                ) : null}
            </div>
        </footer>
    );
}
