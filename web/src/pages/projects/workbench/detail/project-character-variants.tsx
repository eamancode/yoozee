import { App, Button, Checkbox, Input, Modal, Popconfirm, Radio, Select, Upload } from "antd";
import { ChevronDown, Pencil, Plus, Sparkles, Trash2, Upload as UploadIcon, UserRound } from "lucide-react";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { CachedResourceImage } from "@/components/cached-resource-image";
import { AppModal } from "@/components/ui/product/app-modal";
import { getProjectDramaScenes, queueDramaAssetImages, type DramaImageTaskTemplate } from "@/services/api/project-drama";
import { createDramaAssetVariant, deleteDramaAssetVariant, replaceProjectCharacterRepresentations, updateDramaAssetVariant, type DramaAssetVariantInput, type DramaAssetVariantKind, type ProjectAsset } from "@/services/api/projects";
import { resourceFileUrl, uploadResourceFile } from "@/services/api/resources";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { assetImageTaskTemplate } from "./drama-image-task";
import { projectCharacterCover } from "./project-character-cover";
import { turnaroundImageModel } from "./project-character-media";
import { assetImageQueueLabel } from "./project-visual-asset";

const KIND_LABEL: Record<DramaAssetVariantKind, string> = { costume: "服装", state: "状态" };

type VariantForm = { label: string; kind: DramaAssetVariantKind; look: string; promptEn: string; scenes: string[]; image: boolean; startAfter: string };

/** 子卡编辑时的初值：描述和英文提示词用服务端给的原文（只改变体名时外观不算变，三视图保留）。 */
function variantForm(asset?: ProjectAsset): VariantForm {
    if (!asset?.variant) return { label: "", kind: "costume", look: "", promptEn: "", scenes: [], image: true, startAfter: "" };
    return { label: asset.variant.label, kind: asset.variant.kind, look: asset.variant.look, promptEn: asset.variant.promptEn, scenes: asset.variant.scenes, image: false, startAfter: asset.variant.startAfter || "" };
}

/**
 * 角色卡下的子卡（M8 卡 D）：同一角色不同服装 / 状态各一张独立的角色资产「名·变体」，脸型体型继承主卡、只换服装或状态。
 * 折叠显示，可新增（服务端按主卡设定继承生成定义，勾了出图时带主卡三视图作参考出三视图）、改、删（被镜头引用时服务端拒删并列出镜头）、出图。
 */
export function ProjectCharacterVariants({ projectId, parent, defaultImageModel }: { projectId: string; parent: ProjectAsset; defaultImageModel?: string }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const variants = parent.variants || [];
    const [expanded, setExpanded] = useState(false);
    const [preview, setPreview] = useState<{ resourceId: string; title: string } | null>(null);
    const [editing, setEditing] = useState<ProjectAsset | "new" | null>(null);
    const [form, setForm] = useState<VariantForm>(variantForm());
    const scenesQuery = useQuery({ queryKey: ["project", projectId, "drama-scenes", "all"], queryFn: ({ signal }) => getProjectDramaScenes(projectId, signal), enabled: editing !== null });
    const sceneOptions = (scenesQuery.data?.units || []).flatMap((unit) => (unit.episode > 0 ? unit.scenes.map((scene) => ({ value: scene.id, label: `${scene.id} ${scene.heading}` })) : []));

    const imageTask = async (): Promise<DramaImageTaskTemplate> => {
        const model = turnaroundImageModel(effectiveConfig, defaultImageModel);
        const config = { ...effectiveConfig, model };
        if (!model || !isAiConfigReady(config, model)) throw new Error("请先在设置中配置可用的图片模型");
        return assetImageTaskTemplate(config, model);
    };
    const refresh = () => Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: ["project", projectId, "assets"] })]);
    const uploadMutation = useMutation({
        mutationFn: async ({ variant, file }: { variant: ProjectAsset; file: File }) => {
            if (!["image/png", "image/jpeg", "image/webp"].includes(file.type)) throw new Error("请上传 PNG、JPEG 或 WebP 图片");
            const resource = await uploadResourceFile(file, "image", { fileName: file.name });
            return replaceProjectCharacterRepresentations(projectId, variant.id, [{ role: "primary", resourceId: resource.id, metadata: { source: "manual-upload" } }]);
        },
        onSuccess: async () => {
            await refresh();
            message.success("子卡图片已替换并保存为新版本");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "子卡图片替换失败"),
    });
    const saveMutation = useMutation({
        mutationFn: async () => {
            const input: DramaAssetVariantInput = {
                label: form.label.trim(),
                kind: form.kind,
                look: form.look.trim(),
                promptEn: form.promptEn.trim(),
                scenes: form.scenes,
                startAfter: form.startAfter.trim(),
                ...(form.image ? { imageTask: await imageTask() } : {}),
            };
            return editing === "new" || !editing ? createDramaAssetVariant(projectId, parent.id, input) : updateDramaAssetVariant(projectId, editing.id, input);
        },
        onSuccess: async (result) => {
            await refresh();
            setEditing(null);
            setExpanded(true);
            if (result.images.failed.length) message.warning(`子卡「${result.asset.title}」已保存，三视图未排上，请查看卡片上的原因`);
            else if (result.images.waiting.length) message.success(`子卡「${result.asset.title}」已保存，等待主卡三视图或出图空位`);
            else message.success(result.images.queued.length ? `子卡「${result.asset.title}」已保存，三视图已排上` : `子卡「${result.asset.title}」已保存`);
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "子卡保存失败"),
    });
    const deleteMutation = useMutation({
        mutationFn: (variant: ProjectAsset) => deleteDramaAssetVariant(projectId, variant.id),
        onSuccess: async () => {
            await refresh();
            message.success("子卡已移出项目");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "子卡删除失败"),
    });
    const imageMutation = useMutation({
        mutationFn: async (variant: ProjectAsset) => queueDramaAssetImages(projectId, { assetIds: [variant.id], imageTask: await imageTask() }),
        onSuccess: async (result) => {
            await refresh();
            if (result.failed.length) message.warning("子卡没排上出图，原因写在卡片上");
            else message.success(result.waiting.length ? "子卡等主卡三视图或空位，出完会接着出" : "子卡三视图已排上（带主卡三视图作参考）");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "子卡出图失败"),
    });
    const open = (target: ProjectAsset | "new") => {
        setForm(variantForm(target === "new" ? undefined : target));
        setEditing(target);
    };
    const valid = form.label.trim() && form.look.trim() && !/[·・•]/.test(form.label);

    return (
        <div className="mt-2 border-t border-border/60 pt-2">
            <div className="flex items-center justify-between gap-2">
                <button
                    type="button"
                    className="flex min-w-0 items-center gap-1 text-[var(--fs-tiny)] font-medium text-foreground/62 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--workspace-accent)]"
                    onClick={() => setExpanded((value) => !value)}
                    aria-expanded={expanded}
                    disabled={!variants.length}
                >
                    <ChevronDown className={`size-3.5 transition-transform motion-reduce:transition-none ${expanded ? "" : "-rotate-90"}`} />
                    {variants.length ? `子卡 ${variants.length} 张` : "子卡"}
                </button>
                <Button type="text" size="small" className="!h-6 !px-1.5 !text-[var(--fs-tiny)]" icon={<Plus className="size-3" />} onClick={() => open("new")} aria-label={`给 ${parent.title} 新增子卡（服装或状态）`}>
                    新增子卡
                </Button>
            </div>
            {expanded && variants.length ? (
                <ul className="mt-1.5 grid grid-cols-[minmax(0,1fr)] gap-1.5 pl-0">
                    {variants.map((variant) => {
                        const cover = projectCharacterCover(variant.character?.representations);
                        const ready = variant.character?.visualStatus === "ready";
                        const queue = assetImageQueueLabel(variant.image);
                        return (
                            <li key={variant.id} className="min-w-0 list-none rounded-md border border-border/60 px-2 py-1.5">
                                <div className="flex min-w-0 items-center gap-2">
                                    <button
                                        type="button"
                                        disabled={!cover}
                                        aria-label={`预览子卡「${variant.title}」`}
                                        onClick={() => cover && setPreview({ resourceId: cover.resourceId, title: variant.title })}
                                        className="grid size-8 shrink-0 place-items-center overflow-hidden rounded border border-border/60 bg-foreground/[.035] text-foreground/24 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-default"
                                    >
                                        {cover ? (
                                            <CachedResourceImage
                                                storageKey={`resource:${cover.resourceId}`}
                                                src={resourceFileUrl(cover.resourceId)}
                                                alt={variant.title}
                                                loading="lazy"
                                                className="h-full w-full object-contain"
                                                fallback={<UserRound className="size-4" />}
                                            />
                                        ) : (
                                            <UserRound className="size-4" />
                                        )}
                                    </button>
                                    <div className="min-w-0 flex-1">
                                        <div className="truncate text-[var(--fs-label)] font-medium text-foreground/85" title={variant.title}>
                                            {variant.variant?.label || variant.title}
                                            <span className="ml-1.5 text-[var(--fs-micro)] font-normal text-foreground/45">{variant.variant ? KIND_LABEL[variant.variant.kind] : ""}</span>
                                        </div>
                                        <div className="truncate text-[var(--fs-micro)] text-foreground/45" title={variant.variant?.scenes.join("、")}>
                                            {variant.variant?.scenes.length ? `用于 ${variant.variant.scenes.join("、")}` : "还没指定场次"}
                                        </div>
                                        {variant.variant?.startAfter ? <p className="mt-1 text-xs text-foreground/60">动作后生效：{variant.variant.startAfter}。需按镜头选择，不覆盖整场。</p> : null}
                                    </div>
                                </div>
                                <div className="mt-1 flex min-w-0 items-center gap-0.5">
                                    <span className={`min-w-0 flex-1 truncate text-[var(--fs-micro)] ${ready ? "text-emerald-700 dark:text-emerald-300" : "text-foreground/45"}`} title={variant.image?.error || undefined}>
                                        {queue ? `三视图${queue}${variant.image?.error ? `：${variant.image.error}` : ""}` : ready ? "三视图已绑定" : "三视图未出"}
                                    </span>
                                    <Button
                                        type="text"
                                        size="small"
                                        className="!h-6 !px-1"
                                        icon={<Sparkles className="size-3.5" />}
                                        loading={imageMutation.isPending && imageMutation.variables?.id === variant.id}
                                        onClick={() => imageMutation.mutate(variant)}
                                        aria-label={`${ready ? "重新出" : "出"}子卡「${variant.title}」的三视图`}
                                    />
                                    <Button type="text" size="small" className="!h-6 !px-1" icon={<Pencil className="size-3.5" />} onClick={() => open(variant)} aria-label={`编辑子卡「${variant.title}」`} />
                                    <Upload
                                        accept="image/png,image/jpeg,image/webp"
                                        showUploadList={false}
                                        disabled={uploadMutation.isPending}
                                        beforeUpload={(file) => {
                                            uploadMutation.mutate({ variant, file });
                                            return false;
                                        }}
                                    >
                                        <Button
                                            type="text"
                                            size="small"
                                            icon={<UploadIcon className="size-3.5" />}
                                            loading={uploadMutation.isPending && uploadMutation.variables?.variant.id === variant.id}
                                            aria-label={`上传替换子卡「${variant.title}」的图片`}
                                        >
                                            上传替换
                                        </Button>
                                    </Upload>
                                    <Popconfirm title="移出这张子卡？" description="被镜头引用时不能移出，会列出引用它的镜头。" okText="移出" cancelText="取消" onConfirm={() => deleteMutation.mutate(variant)}>
                                        <Button
                                            type="text"
                                            danger
                                            size="small"
                                            className="!h-6 !px-1"
                                            loading={deleteMutation.isPending && deleteMutation.variables?.id === variant.id}
                                            icon={<Trash2 className="size-3.5" />}
                                            aria-label={`移出子卡「${variant.title}」`}
                                        />
                                    </Popconfirm>
                                </div>
                            </li>
                        );
                    })}
                </ul>
            ) : null}
            <AppModal open={Boolean(preview)} onCancel={() => setPreview(null)} footer={null} title={preview?.title || "子卡图片预览"} width={960}>
                {preview ? (
                    <CachedResourceImage
                        key={preview.resourceId}
                        storageKey={`resource:${preview.resourceId}`}
                        src={resourceFileUrl(preview.resourceId)}
                        alt={`${preview.title}完整图片`}
                        eager
                        className="block max-h-[75vh] w-full object-contain"
                        loadingFallback={<p role="status">正在加载完整图片…</p>}
                        fallback={<p role="alert">图片暂时无法加载，请关闭后重新打开。</p>}
                    />
                ) : null}
            </AppModal>
            <Modal
                className="library-modal"
                title={editing === "new" ? `给「${parent.title}」新增子卡` : `编辑子卡「${editing?.title || ""}」`}
                open={editing !== null}
                width={520}
                okText="保存"
                cancelText="取消"
                okButtonProps={{ disabled: !valid, loading: saveMutation.isPending }}
                onCancel={() => setEditing(null)}
                onOk={() => saveMutation.mutate()}
                styles={{ body: { paddingTop: 12 } }}
            >
                <div className="grid gap-3 text-xs">
                    <p className="leading-5 text-foreground/55">子卡继承「{parent.title}」的稳定身份特征；服装、年龄阶段和临时状态以子卡明确的差异为准。生成时携带主卡参考，也可以手动上传图片替换。</p>
                    <label className="grid gap-1.5">
                        <span className="font-medium text-foreground/68">变体名</span>
                        <Input value={form.label} maxLength={24} placeholder="如 西服、淋湿、受伤" onChange={(event) => setForm((current) => ({ ...current, label: event.target.value }))} status={/[·・•]/.test(form.label) ? "error" : undefined} />
                    </label>
                    <div className="grid gap-1.5">
                        <span className="font-medium text-foreground/68">种类</span>
                        <Radio.Group
                            value={form.kind}
                            onChange={(event) => setForm((current) => ({ ...current, kind: event.target.value as DramaAssetVariantKind }))}
                            options={[
                                { value: "costume", label: "服装" },
                                { value: "state", label: "状态" },
                            ]}
                            optionType="button"
                            size="small"
                        />
                    </div>
                    <label className="grid gap-1.5">
                        <span className="font-medium text-foreground/68">{form.kind === "state" ? "状态描述" : "服装描述"}</span>
                        <Input.TextArea
                            rows={3}
                            maxLength={600}
                            value={form.look}
                            placeholder={form.kind === "state" ? "如 护士服湿透贴身，发梢滴水" : "如 深灰西服套装，白衬衫"}
                            onChange={(event) => setForm((current) => ({ ...current, look: event.target.value }))}
                        />
                    </label>
                    <label className="grid gap-1.5">
                        <span className="font-medium text-foreground/68">英文三视图提示词（可空）</span>
                        <Input.TextArea rows={2} value={form.promptEn} placeholder="空着时用主卡的三视图提示词接上这套服装 / 状态" onChange={(event) => setForm((current) => ({ ...current, promptEn: event.target.value }))} />
                    </label>
                    <label className="grid gap-1.5">
                        <span className="font-medium text-foreground/68">用在哪几场</span>
                        <Select mode="multiple" allowClear value={form.scenes} options={sceneOptions} loading={scenesQuery.isLoading} placeholder="生成分段时这几场的镜头绑这张子卡" onChange={(scenes) => setForm((current) => ({ ...current, scenes }))} />
                        <label className="mt-3 block text-xs">场中生效动作（仅整场适用时留空）</label>
                        <Input.TextArea value={form.startAfter} onChange={(event) => setForm((current) => ({ ...current, startAfter: event.target.value }))} placeholder="例如：他从抽屉抽出围裙，系上。保留原文动作与段落。" />
                        {form.startAfter ? <p className="mt-2 text-xs text-foreground/60">此子卡仅在指定动作后适用，不会按场次覆盖整场；请在对应镜头选择，避免提前换装。</p> : null}
                    </label>
                    <Checkbox checked={form.image} onChange={(event) => setForm((current) => ({ ...current, image: event.target.checked }))}>
                        {editing === "new" ? "保存后出三视图" : "外观改了时重出三视图"}
                    </Checkbox>
                </div>
            </Modal>
        </div>
    );
}
