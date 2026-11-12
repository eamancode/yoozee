import { Button } from "antd";

import { AppDrawer } from "@/components/ui/product/app-drawer/app-drawer";

import { INBOX_GROUP_LABEL, type InboxGroup, type InboxItem } from "../model/inbox";

const GROUPS: InboxGroup[] = ["must", "confirm", "suggest"];

/**
 * 待办抽屉：要人管的事只在这里出现一次。重试类直接在这里做完（不用跳走）；
 * 要改内容的跳到对应步骤并高亮那一条；建议可以忽略。
 */
export function InboxPanel({
    open,
    items,
    busyId,
    onClose,
    onAct,
    onLocate,
    onIgnore,
}: {
    open: boolean;
    items: InboxItem[];
    busyId?: string;
    onClose: () => void;
    onAct: (item: InboxItem) => void;
    onLocate: (item: InboxItem) => void;
    onIgnore: (item: InboxItem) => void;
}) {
    return (
        <AppDrawer open={open} onClose={onClose} title={`待办（${items.length}）`} width={420}>
            {items.length ? (
                <div className="drama-flow-inbox">
                    {GROUPS.map((group) => {
                        const list = items.filter((item) => item.group === group);
                        if (!list.length) return null;
                        return (
                            <section key={group}>
                                <h3>
                                    {INBOX_GROUP_LABEL[group]} · {list.length}
                                </h3>
                                {list.map((item) => {
                                    const inPlace = item.action.kind === "retry_image" || item.action.kind === "retry_video" || item.action.kind === "continue";
                                    return (
                                        <div key={item.id} className="drama-flow-inbox-item">
                                            <strong>{item.title}</strong>
                                            <p>{item.detail}</p>
                                            <div className="drama-flow-inbox-actions">
                                                <Button size="small" type={group === "suggest" ? "default" : "primary"} loading={busyId === item.id} disabled={Boolean(busyId) && busyId !== item.id} onClick={() => (inPlace ? onAct(item) : onLocate(item))}>
                                                    {item.action.label}
                                                </Button>
                                                {inPlace ? (
                                                    <Button size="small" type="text" onClick={() => onLocate(item)}>
                                                        看一眼
                                                    </Button>
                                                ) : null}
                                                {group === "suggest" ? (
                                                    <Button size="small" type="text" onClick={() => onIgnore(item)}>
                                                        忽略
                                                    </Button>
                                                ) : null}
                                            </div>
                                        </div>
                                    );
                                })}
                            </section>
                        );
                    })}
                </div>
            ) : (
                <div className="drama-flow-empty">
                    <strong>没有要你处理的事</strong>
                    <span>出了问题或者要花钱的时候，会在这里列出来。</span>
                </div>
            )}
        </AppDrawer>
    );
}
