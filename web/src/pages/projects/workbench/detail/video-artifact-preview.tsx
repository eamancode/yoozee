import { useEffect, useState } from "react";
import { Button } from "antd";
import { Film, Play } from "lucide-react";
import { Callout } from "@/components/ui/product/callout";

export function VideoArtifactPreview({ src, title }: { src: string; title: string }) {
    const [started, setStarted] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const [state, setState] = useState<"loading" | "ready" | "slow" | "error">("loading");

    useEffect(() => {
        setStarted(false);
        setState("loading");
    }, [src]);

    useEffect(() => {
        if (!started || state !== "loading") return;
        const timer = window.setTimeout(() => setState("slow"), 15_000);
        return () => window.clearTimeout(timer);
    }, [started, state, attempt]);

    if (!started)
        return (
            <button
                type="button"
                className="workflow-preview-media-button workflow-video-poster"
                onClick={() => {
                    setState("loading");
                    setStarted(true);
                }}
                aria-label={`点击播放${title}`}
            >
                <span className="workflow-preview-media workflow-video-placeholder" aria-hidden="true">
                    <Film />
                </span>
                <span className="workflow-video-play" aria-hidden="true">
                    <Play className="size-6" fill="currentColor" />
                </span>
            </button>
        );

    return (
        <div className="space-y-2">
            <video
                key={`${src}:${attempt}`}
                className="workflow-preview-media"
                src={src}
                controls
                autoPlay
                playsInline
                preload="auto"
                aria-label={title}
                onLoadedData={() => setState("ready")}
                onPlaying={() => setState("ready")}
                onCanPlay={() => setState("ready")}
                onWaiting={() => setState((current) => (current === "error" ? current : "loading"))}
                onError={() => setState("error")}
            />
            {state === "loading" ? (
                <div role="status" className="text-xs text-foreground/55">
                    正在加载视频，可使用播放器控件开始播放…
                </div>
            ) : null}
            {state === "slow" || state === "error" ? (
                <Callout
                    tone="warning"
                    title={state === "slow" ? "视频加载较慢" : "视频暂时无法播放"}
                    action={
                        <Button
                            size="small"
                            onClick={() => {
                                setState("loading");
                                setAttempt((value) => value + 1);
                            }}
                        >
                            重新加载视频
                        </Button>
                    }
                >
                    {state === "slow" ? "尚未收到可播放画面，请检查网络或浏览器的后台播放限制。" : "请重新加载，或使用下方下载按钮在本地播放器打开。"}重新加载只读取已有文件，不会重新生成或计费。
                </Callout>
            ) : null}
        </div>
    );
}
