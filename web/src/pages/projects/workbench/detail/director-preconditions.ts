import { App } from "antd";
import { API_ERROR_REASON } from "@/services/api/error-codes";
import { ApiError } from "@/services/api/request";

export function useDirectorPrecondition() {
    const { modal } = App.useApp();
    return (error: unknown, handlers: { previous: (id: string) => void; independent: () => void; design: () => void }) => {
        if (!(error instanceof ApiError)) return false;
        if (error.reason === API_ERROR_REASON.EpisodeDesignRequired) {
            modal.confirm({ title: "这一集还没有本集设计", content: error.message, okText: "去剧本阶段", cancelText: "关闭", onOk: handlers.design });
            return true;
        }
        if (error.reason !== API_ERROR_REASON.PreviousHandoffRequired || typeof error.details?.previousUnitId !== "string") return false;
        const id = error.details.previousUnitId;
        modal.confirm({ title: error.message, okText: "独立开局（不接上一集）", cancelText: `先去做${String(error.details.previousTitle || "上一集")}`, onOk: handlers.independent, onCancel: () => handlers.previous(id) });
        return true;
    };
}
