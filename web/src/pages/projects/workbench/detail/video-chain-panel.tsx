import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { App, Button, Image, Spin } from "antd";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { Callout } from "@/components/ui/product/callout";
import { createVideoChain, cancelVideoChain, listVideoChains, previewVideoChain, retryVideoChain, type VideoChain, type VideoChainModel, type VideoChainRequest } from "@/services/api/project-video-chains";
import { resourceFileUrl } from "@/services/api/resources";
import { modelDisplayName } from "@/stores/use-config-store";
import { useDramaModelConfig as useEffectiveConfig } from "@/hooks/use-drama-model-config";
import { ApiError } from "@/services/api/request";
import { getActiveUserScope } from "@/lib/user-scope";
import { useDramaModelPreferencesStore } from "@/stores/use-drama-model-preferences-store";

const phaseLabels: Record<string, string> = {
    waiting: "等待前序镜头",
    extracting: "截取上一组末帧",
    registering: "保存并绑定分镜参考",
    uploading: "上传参考素材",
    prepared: "参考素材已准备",
    reference_ready: "站位参考已准备",
    submitting: "提交生成",
    generating: "等待视频完成",
    completed: "已完成",
    cancelled: "已停止",
};

export function VideoChainPanel({
    open,
    projectId,
	whiteModelStoryboardEnabled,
    unitId,
    shotId,
    buildModel,
    onClose,
    onRefresh,
}: {
    open: boolean;
    projectId: string;
	whiteModelStoryboardEnabled: boolean;
    unitId: string;
    shotId: string;
    buildModel: () => Promise<VideoChainModel>;
    onClose: () => void;
    onRefresh: () => Promise<unknown>;
}) {
    const { message } = App.useApp();
    const config = useEffectiveConfig();
    const [preview, setPreview] = useState<VideoChain>();
    const [selectedId, setSelectedId] = useState<string>();
    const [busy, setBusy] = useState(false);
    const [problem, setProblem] = useState("");
    const request = useRef<VideoChainRequest | undefined>(undefined);
    const prepareRequest = useRef<VideoChainRequest | undefined>(undefined);
    const gate = useRef(false);
    const session = useRef(0);
    const chains = useQuery({ queryKey: ["project-video-chains", projectId, unitId], queryFn: ({ signal }) => listVideoChains(projectId, unitId, signal), enabled: open, refetchInterval: open ? 2000 : false });
    const existing =
        chains.data?.chains.find((chain) => chain.id === selectedId) ||
        chains.data?.chains.find((chain) => ["queued", "running"].includes(chain.status)) ||
        (!preview ? chains.data?.chains.find((chain) => chain.steps.some((step) => step.shotId === shotId)) : undefined);
    const chain = existing || preview;
    const active = chain?.status === "queued" || chain?.status === "running";
    const referenceAttachments = chain?.steps.map((step) => step.frameAssetId || "").join(",") || "";

    useEffect(() => {
        session.current++;
        setPreview(undefined);
        setSelectedId(undefined);
        setProblem("");
        request.current = undefined;
        prepareRequest.current = undefined;
    }, [open, projectId, unitId, shotId]);
	useEffect(() => {
		setPreview(undefined);
		request.current = undefined;
		prepareRequest.current = undefined;
	}, [whiteModelStoryboardEnabled]);
    useEffect(() => {
        if (chain?.status === "succeeded" || chain?.status === "failed") void onRefresh().catch(() => setProblem("生成进度已保存，刷新产物列表失败，请稍后刷新页面"));
    }, [chain?.id, chain?.status]);
    useEffect(() => {
        if (referenceAttachments.replaceAll(",", "")) void onRefresh().catch(() => setProblem("白模附件已保存，刷新分镜图失败，请稍后刷新页面"));
    }, [chain?.id, referenceAttachments]);

    const run = async (action: () => Promise<void>) => {
        if (gate.current) return;
        gate.current = true;
        setBusy(true);
        setProblem("");
        try {
            await action();
        } catch (error) {
            if (error instanceof ApiError && typeof error.details?.activeVideoChainId === "string") {
                setSelectedId(error.details.activeVideoChainId);
                setPreview(undefined);
                await chains.refetch();
            } else if (error instanceof ApiError && error.status === 409) {
                const refreshed = await chains.refetch();
                const activeChain = refreshed.data?.chains.find((item) => item.status === "queued" || item.status === "running");
                if (activeChain) { setSelectedId(activeChain.id); setPreview(undefined); }
            }
            setProblem(error instanceof Error ? error.message : "连续生成操作失败，请重试");
        } finally {
            gate.current = false;
            setBusy(false);
        }
    };
    const refreshPreview = () =>
        run(async () => {
            const version = session.current;
            const model = await buildModel();
            const input = { unitId, shotId, model, requestId: crypto.randomUUID() };
            const { chain } = await previewVideoChain(projectId, input);
            if (version !== session.current) return;
            request.current = { ...input, previewHash: chain.previewHash };
            setSelectedId(undefined);
            setPreview(chain);
        });
    const start = () =>
        run(async () => {
            if (!request.current) throw new Error("请先预览镜头组与费用");
            const scope = getActiveUserScope();
            // Keep this idempotency key after timeouts so a repeated click finds the
            // original controller rather than scheduling another paid chain.
            const { chain } = await createVideoChain(projectId, request.current);
            useDramaModelPreferencesStore.getState().remember("video", request.current.model.model, scope);
            setSelectedId(chain.id);
            setPreview(undefined);
            await chains.refetch();
            message.success("连续生成已启动，关闭面板后仍会在后台继续");
        });

    const preparePosition = () =>
        run(async () => {
            const model = await buildModel();
            const input = { unitId, shotId, model, scope: "single" as const, prepareOnly: true };
            const { chain: preview } = await previewVideoChain(projectId, input);
            const requestId = prepareRequest.current?.previewHash === preview.previewHash ? prepareRequest.current.requestId : crypto.randomUUID();
            prepareRequest.current = { ...input, previewHash: preview.previewHash, requestId };
            const { chain } = await createVideoChain(projectId, prepareRequest.current);
            prepareRequest.current = undefined;
            setSelectedId(chain.id);
            setPreview(undefined);
            await chains.refetch();
            message.success("仅准备站位参考，不会提交视频生成任务");
        });

    return (
        <AppModal open={open} centered title="同场景连续生成" width={820} onCancel={onClose} footer={null} styles={{ body: { maxHeight: "calc(100dvh - 160px)", overflowY: "auto" } }}>
            <p className="mb-4 text-foreground/65">按叙事顺序串联当前场景的连续镜头组，后一组等待前一组视频完成。开启白模分镜时尝试传递片末站位参考；缺失或准备失败时保留原参考图继续。切换场景时结束本链。</p>
			<p className="mb-4 text-xs text-foreground/55">白模分镜：{(chain?.whiteModelStoryboardEnabled ?? whiteModelStoryboardEnabled) ? "开启。片末最后 1 秒硬切至 45° 俯视白模大全景，最后一组不添加片末结束段。" : "关闭。本次不自动添加白模结束段或传递片末站位参考。"}{chain?.id ? "（本链提交时设置）" : "（当前项目设置）"}</p>
            {problem || chains.error ? (
                <div role="alert" className="mb-3">
                    <Callout tone="error">{problem || (chains.error as Error).message}</Callout>
                </div>
            ) : null}
            {chain?.error ? (
                <div role="alert" className="mb-3">
                    <Callout tone="error">{chain.error}。后续镜头暂停，可从失败步骤继续。</Callout>
                </div>
            ) : null}
            {chains.isLoading ? <Spin /> : null}
            {chain ? (
                <>
                    <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                        <strong aria-live="polite">{chain.stage || "提交预览"}</strong>
                        <span>
                            共 {chain.steps.length} 组 · {chain.prepareOnly ? "仅准备参考，不生成视频" : chain.steps.some((step) => step.quote) ? `${chain.totalMicrocredits / 1000000} 平台积分（整链预估，按实际子任务结算）` : "个人渠道按上游计费"}
                        </span>
                    </div>
                    <ol className="max-h-[50vh] space-y-3 overflow-y-auto pr-2 thin-scrollbar">
                        {chain.steps.map((step, index) => (
                            <li key={step.shotId} className="rounded-lg border border-border p-3">
                                <div className="flex flex-wrap justify-between gap-2">
                                    <strong>
                                        {index + 1}. {step.title || "未命名镜头组"}
                                    </strong>
                                    <span>
                                        {step.status === "failed"
                                            ? "失败"
                                            : step.status === "succeeded"
                                              ? "已完成"
                                              : chain.status === "cancelled" && step.status === "waiting"
                                                ? "已停止"
                                                : index === 0 && step.phase === "waiting"
                                                  ? "等待开始"
                                                  : phaseLabels[step.phase] || step.phase}
                                    </span>
                                </div>
                                <p className="mt-1 text-sm text-foreground/65">
                                    {step.scene || "未指定场景"} · 内容 {step.contentSeconds} 秒 → 生成 {step.generationSeconds} 秒 · {modelDisplayName(config, step.model)}
                                </p>
                                <p className="mt-1 text-xs text-foreground/55">
                                    {step.sourceShotId && !step.referenceSkipped ? `保留 ${step.referenceCount} 张原参考图，尝试新增站位图 ${step.referenceTag}` : `保留 ${step.referenceCount} 张参考图`} · {step.ending ? "追加片末结束段" : "不追加片末结束段"}
                                </p>
                                {step.referenceWarning ? <p role="status" className="mt-2 text-sm text-foreground/65">{step.referenceWarning}</p> : null}
                                {step.error ? (
                                    <p role="alert" className="mt-2 text-red-600 dark:text-red-400">
                                        {step.error}
                                    </p>
                                ) : null}
                                {step.frameResourceId ? (
                                    <div className="mt-2">
                                        <Image width={120} src={resourceFileUrl(step.frameResourceId)} alt={`来自上一组的站位参考 ${step.referenceTag}`} />
                                        <span className="ml-2 text-xs text-foreground/55">上一组末帧 · {step.referenceTag}</span>
                                        {step.frameAssetId ? (
                                            <a className="ml-2 text-xs underline" href={`/projects/${projectId}/assets?category=storyboard_reference`}>
                                                在分镜参考资产中查看
                                            </a>
                                        ) : null}
                                    </div>
                                ) : null}
                                {step.videoResourceId ? (
                                    <a className="mt-2 inline-block text-primary underline" href={resourceFileUrl(step.videoResourceId)} target="_blank" rel="noreferrer">
                                        查看本组视频
                                    </a>
                                ) : null}
                            </li>
                        ))}
                    </ol>
                </>
            ) : (
                <p className="py-5 text-foreground/60">先预览范围、参考图数量和费用，再启动生成。</p>
            )}
            <div className="mt-5 flex flex-wrap justify-end gap-2">
                <Button onClick={onClose}>关闭</Button>
                {(active || chain?.status === "failed") && chain?.id ? (
                    <Button
                        danger
                        loading={busy}
                        onClick={() =>
                            void run(async () => {
                                await cancelVideoChain(projectId, chain.id!);
                                await chains.refetch();
                            })
                        }
                    >
                        停止后续生成
                    </Button>
                ) : null}
                {chain?.status === "failed" && chain.id ? (
                    <Button
                        type="primary"
                        loading={busy}
                        onClick={() =>
                            void run(async () => {
                                await retryVideoChain(projectId, chain.id!);
                                await chains.refetch();
                            })
                        }
                    >
                        从失败步骤继续
                    </Button>
                ) : null}
                {!active && whiteModelStoryboardEnabled ? (
                    <Button loading={busy} onClick={() => void preparePosition()}>
                        仅准备本组站位参考
                    </Button>
                ) : null}
                {!active ? (
                    <Button loading={busy} onClick={() => void refreshPreview()}>
                        {chain?.status === "failed" ? "预览新任务与费用" : "预览当前场景与费用"}
                    </Button>
                ) : null}
                {chain?.status === "preview" ? (
                    <Button type="primary" disabled={busy} loading={busy} onClick={() => void start()}>
                        开始连续生成
                    </Button>
                ) : null}
            </div>
            <p className="mt-3 text-xs text-foreground/55">仅准备站位参考：读取前序已有视频、截帧并准备参考输入，不提交任何视频生成任务。</p>
            {active && !chain?.prepareOnly ? <p className="mt-3 text-xs text-foreground/55">已提交的镜头仍会完成；停止后续生成不会撤销已产生的模型费用。关闭面板不会停止生成链。</p> : null}
        </AppModal>
    );
}
