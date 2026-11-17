import { useRef } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Form, Input } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { plainTextToHtml } from "@/lib/project-source";
import { appendProjectUnits } from "@/pages/projects/workbench/detail/project-cache";
import { createProjectUnit, deleteProjectUnit } from "@/services/api/projects";

type EpisodeFields = { title: string; sourceText?: string };
export type EpisodeDialogTarget = { kind: "create" } | { kind: "delete"; id: string; title: string };

export function EpisodeDialog({ projectId, target, onClose, onSaved }: { projectId: string; target: EpisodeDialogTarget; onClose: () => void; onSaved: (createdId?: string) => void }) {
    const client = useQueryClient();
    const { message } = App.useApp();
    const submitting = useRef(false);
    const change = useMutation({
        mutationFn: async (fields?: EpisodeFields) => {
            if (target.kind === "delete") {
                await deleteProjectUnit(projectId, target.id);
                return;
            }
            if (!fields?.title.trim()) throw new Error("请输入剧集标题");
            const { unit } = await createProjectUnit(projectId, { kind: "episode", title: fields.title.trim(), sourceText: plainTextToHtml(fields.sourceText || "") });
            return unit;
        },
        onSuccess: async (created) => {
            const key = ["project", projectId, "units"];
            // 已确认的写入先进入索引，再切换地址；较早发起的列表读取不能把旧索引写回来。
            await client.cancelQueries({ queryKey: key, exact: true });
            client.setQueryData(key, (current: Parameters<typeof appendProjectUnits>[0]) => {
                if (!current || target.kind !== "delete") return created ? appendProjectUnits(current, [created]) : current;
                const canvasCounts = { ...current.canvasCounts };
                delete canvasCounts[target.id];
                return { ...current, units: current.units.filter((unit) => unit.id !== target.id), canvasCounts };
            });
            void client.invalidateQueries({
                queryKey: ["project", projectId],
                predicate: (query) => target.kind !== "delete" || !query.queryKey.slice(2).includes(target.id),
            });
            void client.invalidateQueries({ queryKey: ["drama-flow", "summary", projectId] });
            void client.invalidateQueries({ queryKey: ["projects"] });
        },
        onSettled: () => {
            submitting.current = false;
        },
    });
    const submit = (fields?: EpisodeFields) => {
        if (submitting.current) return;
        submitting.current = true;
        change.mutate(fields, {
            onSuccess: (created) => {
                onSaved(created?.id);
                message.success(created ? "剧集已创建" : "剧集已删除");
            },
        });
    };
    const close = () => {
        if (!submitting.current) onClose();
    };
    const creating = target.kind === "create";
    const error = change.isError ? (
        <p role="alert" className="drama-flow-note" data-tone="error">
            {change.error.message || (creating ? "创建剧集失败" : "删除剧集失败")}
        </p>
    ) : null;
    return (
        <AppModal open width={480} title={creating ? "添加剧集" : "删除此剧集"} onCancel={close} closable={!change.isPending} footer={null}>
            {creating ? (
                <Form layout="vertical" onFinish={submit}>
                    <Form.Item name="title" label="剧集标题" rules={[{ required: true, whitespace: true, message: "请输入剧集标题" }]}>
                        <Input autoFocus placeholder="例如：雨夜归城" />
                    </Form.Item>
                    <Form.Item name="sourceText" label="正文（可选）">
                        <Input.TextArea rows={5} placeholder="创建后可在剧本页继续编辑" />
                    </Form.Item>
                    {error}
                    <div className="flex justify-end gap-2">
                        <Button onClick={close} disabled={change.isPending}>
                            取消
                        </Button>
                        <Button type="primary" htmlType="submit" loading={change.isPending}>
                            创建剧集
                        </Button>
                    </div>
                </Form>
            ) : (
                <>
                    <p>将删除「{target.title}」的剧集正文、镜头表和全部分镜段。已关联的画布会保留在项目中。</p>
                    {error}
                    <div className="mt-4 flex justify-end gap-2">
                        <Button onClick={close} disabled={change.isPending}>
                            取消
                        </Button>
                        <Button danger type="primary" onClick={() => submit()} loading={change.isPending}>
                            删除剧集
                        </Button>
                    </div>
                </>
            )}
        </AppModal>
    );
}
