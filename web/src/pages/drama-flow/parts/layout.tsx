import { useEffect, type ReactNode } from "react";
import { Button } from "antd";
import { X } from "lucide-react";

/**
 * 一步的工作区：左边是产物，右边是选中项的详情栏。详情栏不盖住产物，宽屏时并排、窄屏时从右侧滑出。
 * Esc 收起详情栏（正在输入或开着弹窗时不收）。
 */
export function StageLayout({ pane, children }: { pane?: ReactNode; children: ReactNode }) {
    return (
        <div className="drama-flow-stage" data-pane={pane ? "on" : undefined}>
            <div className="drama-flow-stage-body">{children}</div>
            {pane}
        </div>
    );
}

export function DetailPane({ title, subtitle, onClose, children, actions }: { title: ReactNode; subtitle?: ReactNode; onClose: () => void; children: ReactNode; actions?: ReactNode }) {
    useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.key !== "Escape" || event.defaultPrevented) return;
            const target = event.target as HTMLElement | null;
            if (target?.closest("input, textarea, [contenteditable='true']") || document.querySelector(".ant-modal-wrap:not([style*='display: none'])")) return;
            onClose();
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [onClose]);
    return (
        <aside className="drama-flow-pane" aria-label="详情">
            <header className="drama-flow-pane-head">
                <div className="min-w-0">
                    <h3>{title}</h3>
                    {subtitle ? <p>{subtitle}</p> : null}
                </div>
                <Button type="text" size="small" aria-label="收起详情" icon={<X className="size-4" />} onClick={onClose} />
            </header>
            {actions ? <div className="drama-flow-pane-actions">{actions}</div> : null}
            <div className="drama-flow-pane-body">{children}</div>
        </aside>
    );
}

/** 详情栏里的一节：小标题加内容；没有内容时写「没填」。 */
export function PaneSection({ label, children, empty = "没填" }: { label: string; children?: ReactNode; empty?: string }) {
    const blank = children === undefined || children === null || children === "" || (Array.isArray(children) && !children.length);
    return (
        <section className="drama-flow-pane-section">
            <span>{label}</span>
            {blank ? <p data-empty="true">{empty}</p> : typeof children === "string" ? <p>{children}</p> : children}
        </section>
    );
}

export type SummaryItem = { label: string; value: ReactNode; tone?: "bad" | "warn" | "ok" | "run" };

/** 汇总行：一组「数字 + 标签」，有问题的标警示色。 */
export function SummaryRow({ items }: { items: SummaryItem[] }) {
    return (
        <dl className="drama-flow-summary">
            {items.map((item) => (
                <div key={item.label} data-tone={item.tone}>
                    <dt>{item.label}</dt>
                    <dd>{item.value}</dd>
                </div>
            ))}
        </dl>
    );
}

export type StageTab<T extends string> = { key: T; label: string; count?: number; warn?: boolean };

/** 一步里的小节切换；选中项用 aria-pressed，记在地址栏 tab 参数里。 */
export function StageTabs<T extends string>({ tabs, active, onChange }: { tabs: StageTab<T>[]; active: T; onChange: (key: T) => void }) {
    return (
        <div className="drama-flow-tabs" role="toolbar" aria-label="小节">
            {tabs.map((tab) => (
                <button key={tab.key} type="button" aria-pressed={tab.key === active} onClick={() => onChange(tab.key)}>
                    {tab.label}
                    {tab.count !== undefined ? <span data-warn={tab.warn || undefined}>{tab.count}</span> : null}
                </button>
            ))}
        </div>
    );
}

/** 读失败时的一行：写明没读到，给「再读一次」。 */
export function ReadError({ what, onRetry }: { what: string; onRetry: () => void }) {
    return (
        <div className="drama-flow-note" data-tone="error">
            {what}没读到。
            <Button size="small" type="link" onClick={onRetry}>
                再读一次
            </Button>
        </div>
    );
}
