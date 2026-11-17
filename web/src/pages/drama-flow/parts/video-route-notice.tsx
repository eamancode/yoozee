import { Button } from "antd";

import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { modelOptionName } from "@/stores/use-config-store";

import type { FlowData } from "../hooks/use-flow-data";
import { resolveModelName } from "../hooks/use-model-names";

export function VideoRouteNotice({ routing }: { routing: FlowData["videoRouting"] }) {
    const config = useDramaModelConfig();
    const { route, issue, saveError, retry } = routing;
    const name = (model: string) => resolveModelName(config.channels, model) || modelOptionName(model);
    const sameName = route && route.fromModel !== route.model && name(route.fromModel) === name(route.model);
    const label = (model: string) => (sameName ? `${name(model)}（${model}）` : name(model));
    return (
        <>
            {route ? (
                <div role="status" className="drama-flow-note block w-full">
                    <strong>{route.status === "unavailable" ? "没有可自动切换的线路" : route.status === "manual" ? "已手选项目视频线路" : "已自动切换项目视频线路"}</strong>
                    <p>
                        {route.fromModel && route.fromModel !== route.model ? <>原线路 {label(route.fromModel)} · </> : null}
                        {route.status === "auto" ? "新线路" : "当前线路"} {label(route.model)}
                    </p>
                    <p>{route.reason}</p>
                    <p>
                        成功率 {route.successRate == null ? "未知" : `${route.successRate}%`} · 采样时间 {route.sampledAt ? new Date(route.sampledAt).toLocaleString() : "暂无"}
                    </p>
                </div>
            ) : null}
            {issue || saveError ? (
                <div role="status" className="drama-flow-note w-full" data-tone="warn">
                    {issue || `项目视频线路保存失败。 ${saveError}`}
                    {retry ? (
                        <Button size="small" type="link" onClick={retry}>
                            再读一次
                        </Button>
                    ) : null}
                </div>
            ) : null}
        </>
    );
}
