import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { App, Button, Image, Spin } from "antd";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { Callout } from "@/components/ui/product/callout";
import { createVideoChain, cancelVideoChain, getVideoChainReceipt, previewVideoChain, retryVideoChain, videoChainsQuery, type VideoChain, type VideoChainModel, type VideoChainRequest, type VideoChainStep } from "@/services/api/project-video-chains";
import { resourceFileUrl } from "@/services/api/resources";
import { modelQuoteDescription } from "@/lib/model-pricing";
import { modelDisplayName } from "@/stores/use-config-store";
import { useDramaModelConfig as useEffectiveConfig } from "@/hooks/use-drama-model-config";
import { ApiError } from "@/services/api/request";
import { assertCurrentUserSession, captureUserSession, getActiveUserScope } from "@/lib/user-scope";
import { useDramaModelPreferencesStore } from "@/stores/use-drama-model-preferences-store";
import { queryFailedVideoProviderTask, recoverGenerationTaskMedia, refreshGenerationTaskStatus } from "@/services/api/task-center";
import { videoChainFailureLabel, videoChainOperationProblem } from "./video-chain-diagnostics";
import { clearPendingVideoReceipt, readPendingVideoReceipt, storePendingVideoReceipt, videoReceiptKey, type PendingVideoReceipt, type VideoRecoveryMode } from "./video-chain-receipt";

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
const submissionLabels: Record<string, string> = { unknown: "未知", not_submitted: "未受理", accepted: "已受理" };
const billingLabels: Record<string, string> = { none: "无记录", refunded: "已退款", settled: "已结算", reserved: "已预授权", running: "结算中", uncertain: "待核对" };

function canReuseOriginal(step: VideoChainStep) {
    return step.status === "succeeded" || (step.diagnostic?.terminalStatus === "succeeded" && ["none", "settled"].includes(step.diagnostic.billingStatus || ""));
}

function originalNeedsReconciliation(step?: VideoChainStep) {
    if (!step || canReuseOriginal(step)) return false;
    const diagnostic = step.diagnostic;
    // The controller allocates a child ID before preflight. It may fail before creating a child.
    if (!diagnostic) return Boolean(step.taskId && step.phase === "generating");
    return diagnostic.submissionOutcome === "unknown" || diagnostic.safeToResubmit !== true ||
        ["uncertain", "reserved", "running", "unknown"].includes(diagnostic.billingStatus || "");
}

export function VideoChainPanel({
    open,
    projectId,
	whiteModelStoryboardEnabled,
    sss = false,
    unitId,
    shotId,
    buildModel,
    modelPicker,
    rememberModel = true,
    onEditShot,
    onClose,
    onRefresh,
}: {
    open: boolean;
    projectId: string;
	whiteModelStoryboardEnabled: boolean;
    sss?: boolean;
    unitId: string;
    shotId: string;
    buildModel: (shotId?: string) => Promise<VideoChainModel>;
    modelPicker?: ReactNode;
    rememberModel?: boolean;
    onEditShot?: (shotId: string) => void;
    onClose: () => void;
    onRefresh: () => Promise<unknown>;
}) {
    const { message } = App.useApp();
    const config = useEffectiveConfig();
    const [preview, setPreview] = useState<VideoChain>();
    const [selectedId, setSelectedId] = useState<string>();
    const [busy, setBusy] = useState(false);
    const [problem, setProblem] = useState<ReturnType<typeof videoChainOperationProblem>>();
    const [queryResult, setQueryResult] = useState("");
    const [pending, setPending] = useState<PendingVideoReceipt>();
    const [receiptCheck, setReceiptCheck] = useState(0);
    const [receiptAbsent, setReceiptAbsent] = useState(false);
    const [storageUnavailable, setStorageUnavailable] = useState(false);
    const [quoteExpired, setQuoteExpired] = useState(false);
    const [confirmedChain, setConfirmedChain] = useState<VideoChain>();
    const [lastSourceId, setLastSourceId] = useState<string>();
    const account = getActiveUserScope();
    const storageKey = videoReceiptKey(account, projectId, unitId);
    const request = useRef<VideoChainRequest | undefined>(undefined);
    const quotedReceipt = useRef("");
    const prepareRequest = useRef<VideoChainRequest | undefined>(undefined);
    const gate = useRef(false);
    const session = useRef(0);
    // 和短剧新工作流共用 key 与缓存形状（{ chains }），见 videoChainsQuery。
    const chains = useQuery({ ...videoChainsQuery(projectId, unitId), enabled: open, refetchInterval: open ? 2000 : false });
    const receipt = useQuery({
        queryKey: ["project-video-chain-receipt", storageKey, pending?.requestId, receiptCheck],
        queryFn: ({ signal }) => getVideoChainReceipt(projectId, unitId, pending!.requestId, signal),
        enabled: open && Boolean(pending),
        retry: false,
        refetchOnWindowFocus: false,
    });
    const existing =
        chains.data?.chains.find((chain) => chain.id === selectedId) ||
        (confirmedChain?.id === selectedId ? confirmedChain : undefined) ||
        chains.data?.chains.find((chain) => ["queued", "running"].includes(chain.status)) ||
        chains.data?.chains.find((chain) => chain.steps.some((step) => step.shotId === shotId));
    const chain = existing?.status === "running" || existing?.status === "queued" ? existing : preview || existing;
    const originalId = request.current?.resumeChainId || request.current?.restartChainId || pending?.sourceId;
    const originalChain = originalId ? chains.data?.chains.find((item) => item.id === originalId) : existing;
    const failed = originalChain?.status === "failed" && originalChain.steps.some((step) => step.shotId === shotId) ? originalChain : undefined;
    const originalStep = failed?.steps.find((step) => step.status !== "succeeded");
    const originalDiagnostic = originalStep?.diagnostic;
    const reuseOnly = Boolean(failed?.steps.length && failed.steps.every(canReuseOriginal));
    const receiptVersion = JSON.stringify([failed?.id, originalStep?.taskId, originalDiagnostic?.attemptId, originalDiagnostic?.terminalStatus,
        originalDiagnostic?.submissionOutcome, originalDiagnostic?.billingStatus, originalDiagnostic?.safeToResubmit]);
    const receiptChanged = Boolean((request.current?.resumeChainId || request.current?.restartChainId) && receiptVersion !== quotedReceipt.current);
    const originalPending = originalNeedsReconciliation(originalStep);
    const quotePending = preview?.steps.some((step) => step.diagnostic && originalNeedsReconciliation(step));
    const blockedReason = quoteExpired ? "报价已过期，请重新报价后确认。" : receiptChanged && !originalPending ? "原请求的状态已变化，请重新报价后确认。" : preview?.confirmationBlockedReason ||
        (originalPending || quotePending ? "原请求的受理或结算尚未核实。请先查询原请求，再重新报价并确认。" :
        preview && !preview.previewHash?.trim() ? "缺少有效报价标识，请重新报价后确认。" : "");
    const active = chain?.status === "queued" || chain?.status === "running";
    const awaitingReceipt = Boolean(pending && (!receiptAbsent || receipt.isFetching));
    const pendingOtherShot = Boolean(pending && pending.shotId !== shotId);
    const recoveryMode: VideoRecoveryMode = request.current?.restartChainId ? "restart" : request.current?.resumeChainId ? "resume" : "fresh";
    const quoteControlsDisabled = awaitingReceipt || pendingOtherShot || storageUnavailable || receipt.isFetching || chains.isError || chains.isRefetchError;
    const previousId = chain?.sourceChainId || lastSourceId;
    const priorError = chain?.status !== "preview" && previousId
        ? chains.data?.chains.find((item) => item.id === previousId)?.error
        : failed?.error;
    const newError = (previousId || (selectedId && selectedId === chain?.id)) && chain?.status === "failed" ? chain.error : undefined;
    const referenceAttachments = chain?.steps.map((step) => step.frameAssetId || "").join(",") || "";
    const originalVideos = new Map<string, VideoChainStep>();
    for (const history of chains.data?.chains || []) {
        for (const step of history.steps) {
            if (step.videoResourceId && (step.shotId === shotId || failed?.steps.some((item) => item.shotId === step.shotId))) originalVideos.set(step.videoResourceId, step);
        }
    }

    useEffect(() => {
        session.current++;
        setPreview(undefined);
        setSelectedId(undefined);
        setConfirmedChain(undefined);
        setLastSourceId(undefined);
        setProblem(undefined);
        setQueryResult("");
        request.current = undefined;
        prepareRequest.current = undefined;
        return () => { session.current++; };
    }, [open, projectId, unitId, shotId, account]);
    useEffect(() => {
        if (!open) return;
        const reloadPending = () => {
            setReceiptAbsent(false);
            try {
                setPending(readPendingVideoReceipt(window.localStorage, storageKey));
                setStorageUnavailable(false);
            } catch {
                setStorageUnavailable(true);
            }
        };
        reloadPending();
        const onStorage = (event: StorageEvent) => {
            if (event.key === storageKey || event.key === null) reloadPending();
        };
        window.addEventListener("storage", onStorage);
        return () => window.removeEventListener("storage", onStorage);
    }, [open, storageKey, shotId]);
    useEffect(() => {
        if (!open || !pending || !receipt.data || receipt.isFetching) return;
        if (receipt.data.state === "absent") {
            setReceiptAbsent(true);
            return;
        }
        const recovered = receipt.data.chain;
        if (!recovered?.id || !Array.isArray(recovered.steps)) return;
        try {
            clearPendingVideoReceipt(window.localStorage, storageKey, pending.requestId);
        } catch {
            setStorageUnavailable(true);
        }
        request.current = undefined;
        setPreview(undefined);
        setPending(undefined);
        setReceiptAbsent(false);
        setSelectedId(recovered.id);
        setConfirmedChain(recovered);
        setLastSourceId(pending.sourceId);
        setProblem(undefined);
        setQueryResult("已找到本次提交回执，未重复提交。");
    }, [open, pending, receipt.data, receipt.isFetching, storageKey]);
    useEffect(() => {
        setQuoteExpired(false);
        if (!preview?.quoteExpiresAt) return;
        const remaining = preview.quoteExpiresAt * 1000 - Date.now();
        if (remaining <= 0) {
            setQuoteExpired(true);
            return;
        }
        const timer = window.setTimeout(() => setQuoteExpired(true), remaining);
        return () => window.clearTimeout(timer);
    }, [preview?.quoteExpiresAt]);
	useEffect(() => {
		session.current++;
		setPreview(undefined);
		request.current = undefined;
		prepareRequest.current = undefined;
	}, [whiteModelStoryboardEnabled]);
    useEffect(() => {
        if (chain?.status === "succeeded" || chain?.status === "failed") void onRefresh().catch((error) => setProblem(videoChainOperationProblem(error, "刷新产物列表")));
    }, [chain?.id, chain?.status]);
    useEffect(() => {
        if (referenceAttachments.replaceAll(",", "")) void onRefresh().catch((error) => setProblem(videoChainOperationProblem(error, "刷新分镜图")));
    }, [chain?.id, referenceAttachments]);

    const run = async (operation: string, action: () => Promise<void>) => {
        if (gate.current) return;
        gate.current = true;
        setBusy(true);
        setProblem(undefined);
        const version = session.current;
        try {
            await action();
        } catch (error) {
            if (version !== session.current) return;
            if (operation === "确认提交" && error instanceof ApiError && error.status && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429) {
                request.current = undefined;
                setPreview(undefined);
            }
            if (error instanceof ApiError && typeof error.details?.activeVideoChainId === "string") {
                setSelectedId(error.details.activeVideoChainId);
                setPreview(undefined);
                await chains.refetch();
            } else if (error instanceof ApiError && error.status === 409) {
                const refreshed = await chains.refetch();
                const activeChain = refreshed.data?.chains.find((item) => item.status === "queued" || item.status === "running");
                if (activeChain) { setSelectedId(activeChain.id); setPreview(undefined); }
            }
            setProblem(videoChainOperationProblem(error, operation));
        } finally {
            gate.current = false;
            setBusy(false);
        }
    };
    const refreshPreview = (mode: VideoRecoveryMode) =>
        run(mode === "restart" ? "从头生成报价" : mode === "resume" ? "失败段重新报价" : "本次费用预览", async () => {
            const version = session.current;
            const scope = captureUserSession();
            if (scope.scope !== account) return;
            const prior = readPendingVideoReceipt(window.localStorage, storageKey);
            if (prior && (prior.requestId !== pending?.requestId || !receiptAbsent || prior.shotId !== shotId ||
                prior.mode !== mode || prior.sourceId !== (mode === "fresh" ? undefined : failed?.id))) {
                throw new Error("该集已有待查的提交回执。请核对后回到原镜头继续。");
            }
            request.current = undefined;
            setPreview(undefined);
            if (mode !== "fresh" && !failed?.id) throw new Error("找不到原生成记录，请刷新后重试");
            const firstShot = failed?.anchorShotId || failed?.steps[0]?.shotId || shotId;
            const model = mode === "resume" && reuseOnly && failed
                ? { model: (originalStep || failed.steps[0]).model, input: {} }
                : await buildModel(mode === "restart" ? firstShot : mode === "resume" ? originalStep?.shotId || shotId : shotId);
            assertCurrentUserSession(scope);
            if (version !== session.current) return;
            const input: VideoChainRequest = { unitId, shotId, model, requestId: prior?.requestId || crypto.randomUUID() };
            if (!failed?.prepareOnly) input.recoveryV2 = true;
            if (mode !== "fresh" && failed?.id) {
                if (mode === "resume") input.resumeChainId = failed.id;
                else input.restartChainId = failed.id;
                input.shotId = firstShot;
                input.scope = failed.scope;
                if (mode === "resume") input.prepareOnly = failed.prepareOnly;
            }
            const { chain } = await previewVideoChain(projectId, input);
            assertCurrentUserSession(scope);
            if (version !== session.current) return;
            request.current = { ...input, previewHash: chain.previewHash, quoteExpiresAt: chain.quoteExpiresAt };
            quotedReceipt.current = receiptVersion;
            setSelectedId(undefined);
            setPreview(chain);
        });
    const start = () =>
        run("确认提交", async () => {
            if (blockedReason || quoteControlsDisabled ||
                !preview?.previewHash || request.current?.previewHash !== preview.previewHash ||
                (preview.quoteExpiresAt && preview.quoteExpiresAt * 1000 <= Date.now())) return;
            const version = session.current;
            const scope = captureUserSession();
            if (scope.scope !== account) return;
            const confirmed = request.current;
            const requestId = confirmed.requestId;
            if (!requestId) return;
            if (confirmed.prepareOnly && confirmed.resumeChainId) {
                // Preparing references is a separate, non-video operation.
                const { chain } = await retryVideoChain(projectId, { ...confirmed, resumeChainId: confirmed.resumeChainId });
                if (version !== session.current || scope.signal.aborted) return;
                request.current = undefined;
                setSelectedId(chain.id);
                setPreview(undefined);
                await chains.refetch();
                return;
            }
            const entry: PendingVideoReceipt = {
                requestId, shotId,
                sourceId: confirmed.resumeChainId || confirmed.restartChainId,
                mode: confirmed.resumeChainId ? "resume" : confirmed.restartChainId ? "restart" : "fresh",
            };
            const submit = async () => {
                assertCurrentUserSession(scope);
                // Persist only opaque identifiers before calling a paid endpoint.
                storePendingVideoReceipt(window.localStorage, storageKey, entry);
                let chain: VideoChain;
                try {
                    ({ chain } = confirmed.resumeChainId
                        ? await retryVideoChain(projectId, { ...confirmed, resumeChainId: confirmed.resumeChainId })
                        : await createVideoChain(projectId, confirmed));
                    if (!chain.id) throw new Error("确认没有返回任务编号");
                } catch (error) {
                    const rejected = error instanceof ApiError && [400, 401, 403, 404, 412, 422].includes(error.status ?? error.code ?? 0);
                    // If this is a replay after an unknown outcome, a rejection of
                    // the replay does not prove the earlier request was rejected.
                    const definitive = rejected && pending?.requestId !== requestId;
                    if (definitive) clearPendingVideoReceipt(window.localStorage, storageKey, requestId);
                    if (version === session.current && !scope.signal.aborted) {
                        setReceiptAbsent(false);
                        setPending(definitive ? undefined : entry);
                        setReceiptCheck((n) => n + 1);
                        if (rejected && !definitive) {
                            request.current = undefined;
                            setPreview(undefined);
                            setProblem(videoChainOperationProblem(error, "确认提交"));
                        }
                    }
                    if (definitive) throw error;
                    // A timeout/network error is not a rejection. The next action is lookup, not a new submit.
                    return;
                }
                clearPendingVideoReceipt(window.localStorage, storageKey, requestId);
                if (version !== session.current || scope.signal.aborted) return;
                if (rememberModel && !(reuseOnly && confirmed.resumeChainId)) useDramaModelPreferencesStore.getState().remember("video", confirmed.model.model, scope.scope);
                request.current = undefined;
                setPending(undefined);
                setReceiptAbsent(false);
                setSelectedId(chain.id);
                setConfirmedChain(Array.isArray(chain.steps) ? chain : undefined);
                setLastSourceId(entry.sourceId);
                setPreview(undefined);
                message.success(chain.status === "queued" ? "已受理，正在排队。" : "已收到提交回执。");
                await chains.refetch();
            };
            if (navigator.locks) {
                await navigator.locks.request(storageKey, { ifAvailable: true }, async (lock) => {
                    if (!lock) throw new Error("另一个页面正在确认，请先核对提交回执");
                    await submit();
                });
            } else await submit();
        });

    const preparePosition = () =>
        run("准备站位参考", async () => {
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

    const queryOriginal = (step: VideoChainStep) => run("查询原请求", async () => {
        if (!step.taskId) return;
        const version = session.current;
        request.current = undefined;
        setPreview(undefined);
        setQueryResult("");
        try {
            const original = await refreshGenerationTaskStatus(step.taskId);
            if (version !== session.current) return;
            const billing = original.billing?.status || step.diagnostic?.billingStatus || "none";
            const billingStatus = billingLabels[billing] || billing;
            if (original.status === "failed" && (step.diagnostic?.canQueryProvider ?? Boolean(original.providerRequestId)) && !original.canRecoverMedia) {
                const result = await queryFailedVideoProviderTask(step.taskId);
                if (version !== session.current) return;
                setQueryResult(result.recovered ? `已取回原视频。结算${result.billingSettled ? "已完成" : "待核对"}。` : `上游状态 ${result.providerStatus === "failed" ? "已确认失败" : result.providerStatus === "cancelled" ? "已确认取消" : result.providerStatus || "待确认"} · 结算 ${billingStatus}`);
            } else {
                setQueryResult(`原任务 ${original.id} · 状态 ${original.status} · 结算 ${billingStatus}${!original.providerRequestId && original.status === "failed" ? " · 尚无上游受理号" : ""}`);
            }
        } finally {
            if (version === session.current) {
                await chains.refetch();
                await onRefresh();
            }
        }
    });
    const recoverOriginal = (taskId: string) => run("恢复原视频保存", async () => {
        const version = session.current;
        request.current = undefined;
        setPreview(undefined);
        await recoverGenerationTaskMedia(taskId);
        if (version !== session.current) return;
        await chains.refetch();
        await onRefresh();
        if (version === session.current) setQueryResult("原视频已开始恢复保存，可查询原请求查看进度。");
    });

    const renderOriginalReceipt = (step: VideoChainStep) => {
        const receipt = step.status === "failed" ? step :
            chain?.status === "preview" && originalStep?.shotId === step.shotId
                ? { ...originalStep, taskId: originalStep.taskId || step.taskId, diagnostic: receiptChanged ? originalDiagnostic : step.diagnostic || originalDiagnostic }
                : undefined;
        if (!receipt) return null;
        return (
            <div className="mt-2 space-y-1 text-sm">
                {receipt.diagnostic ? (
                    <p>
                        {[videoChainFailureLabel(receipt.diagnostic), receipt.diagnostic.upstreamModel, receipt.diagnostic.channelId, receipt.diagnostic.submissionOutcome && `受理状态 ${submissionLabels[receipt.diagnostic.submissionOutcome] || receipt.diagnostic.submissionOutcome}`, receipt.diagnostic.billingStatus && `结算 ${billingLabels[receipt.diagnostic.billingStatus] || receipt.diagnostic.billingStatus}`].filter(Boolean).join(" · ")}
                    </p>
                ) : null}
                {receipt.taskId ? <p className="text-xs text-foreground/65">原任务 {receipt.taskId}</p> : null}
                {receipt.diagnostic?.quoteId ? <p className="break-all text-xs text-foreground/65">原报价 {receipt.diagnostic.quoteId}</p> : null}
                {receipt.diagnostic && (receipt.diagnostic.safeToResubmit === false || receipt.diagnostic.submissionOutcome === "unknown") && receipt.diagnostic.terminalStatus !== "succeeded" ? (
                    <p>{receipt.diagnostic.canRecoverMedia ? "可恢复已有视频的保存。" : receipt.diagnostic.canQueryProvider ? "确认继续生成前，请先查询原请求的上游终态与结算。" : "原请求尚未确认可重发。请按原任务标识核对渠道回执及结算。"}</p>
                ) : null}
                {receipt.taskId ? <Button size="small" loading={busy} onClick={() => void queryOriginal(receipt)}>查询原请求与结算</Button> : null}
                {receipt.taskId && receipt.diagnostic?.canRecoverMedia ? <Button size="small" loading={busy} onClick={() => void recoverOriginal(receipt.taskId!)}>恢复原视频保存</Button> : null}
            </div>
        );
    };

    return (
        <AppModal open={open} centered title="同场景连续生成" width={820} onCancel={onClose} footer={null} styles={{ body: { maxHeight: "calc(100dvh - 160px)", overflowY: "auto" } }}>
            <p className="mb-4 text-foreground/65">按叙事顺序串联当前场景的连续镜头组，后一组等待前一组视频完成。切换场景时结束本链。</p>
            {sss ? <p className="mb-4 text-xs text-foreground/55">同场景上一段以全景定格收尾时，使用其最后 1 秒中间的画面作为站位参考。截帧或上传失败时，保留正文及原参考图继续生成。</p> : <p className="mb-4 text-xs text-foreground/55">白模分镜：{(chain?.whiteModelStoryboardEnabled ?? whiteModelStoryboardEnabled) ? "开启。片末最后 1 秒硬切至 45° 俯视白模大全景，最后一组不添加片末结束段。" : "关闭。本次不自动添加白模结束段或传递片末站位参考。"}{chain?.id ? "（本链提交时设置）" : "（当前项目设置）"}</p>}
            {modelPicker && !active && !reuseOnly ? <div className="mb-3 flex flex-wrap items-center gap-2"><span>本次报价模型</span>{modelPicker}</div> : null}
            {problem || chains.error ? (
                <div role="alert" className="mb-3">
                    <Callout tone="error">{problem?.text || (chains.error as Error).message}</Callout>
                    {problem?.canEditShot && onEditShot ? <Button type="link" size="small" onClick={() => onEditShot(problem.shotId || shotId)}>调整本段时长或参考素材</Button> : null}
                </div>
            ) : null}
            {queryResult ? <p role="status" className="mb-3">{queryResult}</p> : null}
            {storageUnavailable ? <Callout tone="error">无法安全保存提交回执，本次不会提交。请检查浏览器存储后重新打开。</Callout> : null}
            {pending ? (
                <div className="mb-3 flex flex-wrap items-center gap-2" role="status">
                    <span>{receipt.isFetching ? "正在核对提交回执，请勿再次提交。" :
                        receiptAbsent ? pendingOtherShot ? "暂未查到回执。请回原镜头使用同一次确认重试。" : "暂未查到回执，可以使用同一次确认重试；不会创建第二次请求。" :
                        "提交结果未确认。请先核对回执，暂不可再次提交。"}</span>
                    <Button size="small" loading={receipt.isFetching} onClick={() => {
                        setReceiptAbsent(false);
                        setReceiptCheck((n) => n + 1);
                    }}>核对提交回执</Button>
                </div>
            ) : null}
            {originalVideos.size ? (
                <div className="mb-3 flex flex-wrap gap-3 text-sm">
                    <strong>已完成的视频</strong>
                    {[...originalVideos].map(([resourceId, step]) => <a key={resourceId} className="text-primary underline" href={resourceFileUrl(resourceId)} target="_blank" rel="noreferrer">{step.title || step.shotId} · 查看视频</a>)}
                </div>
            ) : null}
            {priorError ? (
                <div role="alert" className="mb-3">
                    <Callout tone="error">原生成链失败：{priorError}</Callout>
                </div>
            ) : null}
            {newError ? <div role="alert" className="mb-3"><Callout tone="error">本次生成失败：{newError}。后续镜头暂停。</Callout></div> : null}
            {chains.isLoading ? <Spin /> : null}
            {chain ? (
                <>
                    <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                        <strong aria-live="polite">{chain.status === "preview" ? (recoveryMode === "restart" ? "从头重新生成 · 本次报价" : recoveryMode === "resume" ? "从失败段继续 · 本次报价" : "本次费用预览") :
                            chain.status === "queued" ? "已受理，正在排队" : chain.status === "running" ? "已受理，正在生成" : chain.stage || "已有生成记录"}</strong>
                        <span>
                            共 {chain.steps.length} 组 · {chain.prepareOnly ? "仅准备参考，不生成视频" : recoveryMode === "resume" && chain.status === "preview" ? `剩余步骤预估 ${chain.totalMicrocredits / 1000000} 平台积分` : chain.steps.some((step) => step.quote) ? `${chain.totalMicrocredits / 1000000} 平台积分（整链预估，按实际子任务结算）` : "个人渠道按上游计费"}
                        </span>
                    </div>
                    {chain.status === "preview" && chain.previewHash ? <div className="mb-2 text-xs text-foreground/60">
                        {recoveryMode === "restart" ? <p>从第一步开始，不复用原视频；保留原记录和已完成的视频。</p> : recoveryMode === "resume" ? <p>从失败处继续，报价中标记可复用的视频不会重新生成。</p> : null}
                        {chain.quoteExpiresAt ? <p>报价有效至 {new Date(chain.quoteExpiresAt * 1000).toLocaleTimeString()}</p> : null}
                        <p className="break-all">报价标识 {chain.previewHash}</p>
                    </div> : null}
                    {chain.status === "preview" && blockedReason ? <div role="status" className="mb-3"><Callout tone="warning">本次仅供估价。{blockedReason}</Callout></div> : null}
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
                                              ? (chain.status === "preview" ? "复用已完成视频" : "已完成")
                                              : chain.status === "failed" && step.status === "waiting"
                                                ? "等待失败步骤恢复"
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
                                    {step.sourceShotId && !step.referenceSkipped ? `保留 ${step.referenceCount} 张原参考图，尝试新增站位图 ${step.referenceTag}` : `保留 ${step.referenceCount} 张参考图`}{step.referenceAudioCount ? ` · 声音参考 ${step.referenceAudioCount} 条` : ""} · {step.ending ? "追加片末结束段" : "不追加片末结束段"}
                                </p>
                                {step.quote && chain.status === "preview" && step.status !== "succeeded" ? <p className="mt-1 text-xs text-foreground/65">{({ fixed_request: "按条", per_second: "按秒", token: "按 Token" })[step.quote.billingMode]} · {step.quote.amountMicrocredits / 1000000} 平台积分</p> : null}
                                {step.quote?.upstreamCost && !chain.prepareOnly ? <p className="mt-1 text-sm text-foreground/65">{modelQuoteDescription(step.quote)}</p> : null}
                                {step.referenceWarning ? <p role="status" className="mt-2 text-sm text-foreground/65">{step.referenceWarning}</p> : null}
                                {step.error ? (
                                    <p role="alert" className="mt-2 text-red-600 dark:text-red-400">
                                        {step.error}
                                    </p>
                                ) : null}
                                {renderOriginalReceipt(step)}
                                {step.frameResourceId ? (
                                    <div className="mt-2">
                                        <Image width={120} src={resourceFileUrl(step.frameResourceId)} alt={`来自上一组的站位参考 ${step.referenceTag}`} />
                                        <span className="ml-2 text-xs text-foreground/55">{step.frameSelection==="freeze-mid" ? "站位参考：来自上一段定格" : "上一组末帧"} · {step.referenceTag}</span>
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
                {active && chain?.id ? (
                    <Button
                        danger
                        loading={busy}
                        onClick={() =>
                            void run("停止后续生成", async () => {
                                await cancelVideoChain(projectId, chain.id!);
                                await chains.refetch();
                            })
                        }
                    >
                        停止后续生成
                    </Button>
                ) : null}
                {!active && !failed && (sss || whiteModelStoryboardEnabled) ? (
                    <Button loading={busy} disabled={Boolean(pending) || storageUnavailable} onClick={() => void preparePosition()}>
                        仅准备本组站位参考
                    </Button>
                ) : null}
                {!active && chains.isSuccess && !chains.isRefetchError && (!failed || failed.steps.length > 1 || failed.prepareOnly || reuseOnly) ? (
                    <Button loading={busy} disabled={quoteControlsDisabled || Boolean(pending && pending.mode !== (failed ? "resume" : "fresh"))}
                        onClick={() => void refreshPreview(failed ? "resume" : "fresh")}>
                        {reuseOnly ? "预览可复用的原视频" : failed ? "从失败步骤重新报价" : "预览当前场景与费用"}
                    </Button>
                ) : null}
                {!active && chains.isSuccess && !chains.isRefetchError && failed && !failed.prepareOnly && !(reuseOnly && failed.steps.length === 1) ? (
                    <Button loading={busy} disabled={quoteControlsDisabled || Boolean(pending && pending.mode !== "restart")}
                        onClick={() => void refreshPreview("restart")}>
                        {failed.steps.length === 1 ? "重新生成整段（先报价）" : "从头重新生成（先报价）"}
                    </Button>
                ) : null}
                {chain?.status === "preview" && request.current ? (
                    <Button type="primary" disabled={busy || Boolean(blockedReason) || quoteControlsDisabled} loading={busy} onClick={() => void start()}>
                        {recoveryMode === "restart" ? "确认报价并从头生成" : recoveryMode === "resume" ? (reuseOnly ? "确认复用原视频" : "确认报价并从失败段继续") : "开始连续生成"}
                    </Button>
                ) : null}
            </div>
            <p className="mt-3 text-xs text-foreground/55">仅准备站位参考：读取前序已有视频、截帧并准备参考输入，不提交任何视频生成任务。</p>
            {active && !chain?.prepareOnly ? <p className="mt-3 text-xs text-foreground/55">已提交的镜头仍会完成；停止后续生成不会撤销已产生的模型费用。关闭面板不会停止生成链。</p> : null}
        </AppModal>
    );
}
