import { useState } from "react";
import { Alert, Checkbox } from "antd";
import { AppModal } from "@/components/ui/product/app-modal";
import { ModelPicker } from "@/components/model-picker";
import type { AiConfig } from "@/stores/use-config-store";
import type { ProjectAsset } from "@/services/api/projects";
import { assetImageQueueLabel } from "./project-visual-asset";

export function AssetImageRecovery({
    assets,
    config,
    defaultModel,
    busy,
    onClose,
    onSubmit,
}: {
    assets: ProjectAsset[];
    config: AiConfig;
    defaultModel: string;
    busy: boolean;
    onClose: () => void;
    onSubmit: (selection: { model: string; assetIds: string[] }) => void;
}) {
    const [model, setModel] = useState(defaultModel);
    const [ids, setIds] = useState(() => assets.filter((asset) => asset.visual?.definition.productionPriority !== "low").map((asset) => asset.id));
    return (
        <AppModal
            open
            title="继续资产出图"
            onCancel={onClose}
            onOk={() => onSubmit({ model, assetIds: ids })}
            okText={`为 ${ids.length} 个资产出图`}
            confirmLoading={busy}
            okButtonProps={{ disabled: !model || !ids.length }}
            cancelButtonProps={{ disabled: busy }}
        >
            <div className="space-y-4">
                <Alert type="info" showIcon title="选择本次图片渠道与资产" description="已成功的图片不会重出。子卡需等待主卡图片，请同时选择尚未出图的主卡。鉴权或余额异常会暂停本批后续任务，可修复渠道后在此继续。" />
                <ModelPicker config={config} capability="image" value={model} onChange={setModel} fullWidth showSelectedPrice />
                <Checkbox checked={ids.length === assets.length} indeterminate={ids.length > 0 && ids.length < assets.length} onChange={(event) => setIds(event.target.checked ? assets.map((asset) => asset.id) : [])}>
                    全选待出图资产
                </Checkbox>
                <div className="max-h-72 space-y-2 overflow-y-auto" aria-label="选择恢复出图的资产">
                    {assets.map((asset) => (
                        <div key={asset.id}>
                            <Checkbox checked={ids.includes(asset.id)} onChange={(event) => setIds((current) => (event.target.checked ? [...current, asset.id] : current.filter((id) => id !== asset.id)))}>
                                {asset.title} · {assetImageQueueLabel(asset.image) || "未出图"}
                                {asset.visual?.definition.productionPriority === "low" ? " · 背景道具（默认暂不出图，可手动选择）" : ""}
                            </Checkbox>
                            {asset.image?.error ? <p className="ml-6 break-words text-xs text-foreground/55">{asset.image.error}</p> : null}
                        </div>
                    ))}
                </div>
            </div>
        </AppModal>
    );
}
