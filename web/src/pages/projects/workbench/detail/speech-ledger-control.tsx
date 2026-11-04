import { useState } from "react";
import { App, Button } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AppModal } from "@/components/ui/product/app-modal";
import { ModelPicker } from "@/components/model-picker";
import { getUnitSpeechLedger } from "@/services/api/project-speech";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import { generateUnitSpeechLedger } from "./project-chapter-ai";

const kindLabel = { dialogue: "台词", voiceover: "画外", narration: "旁白" };

export function SpeechLedgerControl({ projectId, unitId, disabled }: { projectId: string; unitId: string; disabled?: boolean }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const config = useEffectiveConfig();
    const ready = useConfigStore((state) => state.isAiConfigReady);
    const [open, setOpen] = useState(false);
    const [model, setModel] = useState(config.textModel || config.model);
    const [status, setStatus] = useState("");
    const ledger = useQuery({ queryKey: ["project", projectId, "speech-ledger", unitId], queryFn: ({ signal }) => getUnitSpeechLedger(projectId, unitId, signal), enabled: Boolean(unitId) });
    const run = useMutation({
        mutationFn: () => generateUnitSpeechLedger(projectId, unitId, { ...config, model, textModel: model }, { onTaskUpdate: (task) => setStatus(task.stage || "正在提取完整声音台账") }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: ["project", projectId] });
            message.success("声音台账已保存，可核对未落镜的台词、画外音和旁白");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "声音台账提取失败"),
    });
    return (
        <>
            <Button size="small" disabled={disabled || !unitId} onClick={() => setOpen(true)}>
                声音台账{ledger.data?.ready ? " · 已提取" : " · 未提取"}
            </Button>
            <AppModal
                open={open}
                title="台词 / 画外 / 旁白台账"
                onCancel={() => setOpen(false)}
                onOk={() => run.mutate()}
                okText={ledger.data?.ready ? "重新提取" : "独立提取"}
                confirmLoading={run.isPending}
                okButtonProps={{ disabled: !ready({ ...config, model }, model) }}
            >
                <p className="mb-3">从已保存的完整剧本逐段提取，保留原文和重复发言，再与分镜核对。未提取或正文已变化时，当前覆盖率仅为规则扫描。</p>
                <ModelPicker config={config} capability="text" value={model} onChange={setModel} fullWidth />
                {run.isPending ? <p role="status">{status}</p> : null}
                {ledger.isError ? <p role="alert">声音台账读取失败，请重试</p> : null}
                <div className="mt-4 max-h-80 space-y-2 overflow-y-auto">
                    {ledger.data?.ledger?.entries.map((entry, index) => (
                        <div key={`${entry.scene}-${entry.paragraph}-${index}`} className="rounded border border-border p-2">
                            <p className="text-xs text-foreground/55">
                                {entry.scene} · 原文第 {entry.paragraph} 段 · {kindLabel[entry.kind]} · {entry.speaker}
                            </p>
                            <p className="whitespace-pre-wrap">{entry.text}</p>
                        </div>
                    ))}
                </div>
            </AppModal>
        </>
    );
}
