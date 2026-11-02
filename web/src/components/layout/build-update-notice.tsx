import { useEffect, useState } from "react";
import { Button } from "antd";
import { Callout } from "@/components/ui/product/callout";

export function BuildUpdateNotice() {
    const [available, setAvailable] = useState(false);
    useEffect(() => {
        const current = String(import.meta.env.VITE_BUILD_COMMIT || "");
        if (!import.meta.env.PROD || !/^[a-f0-9]{7,64}$/i.test(current)) return;
        let active = true;
        const check = async () => {
            if (document.visibilityState !== "visible") return;
            try {
                const response = await fetch(`${import.meta.env.BASE_URL}build-info.json`, { cache: "no-store", signal: AbortSignal.timeout(10_000) });
                if (!response.ok) return;
                const next = await response.json();
                if (active && /^[a-f0-9]{7,64}$/i.test(next.commit || "") && next.commit !== current) setAvailable(true);
            } catch {
                // A version check must not interrupt editing during a network failure.
            }
        };
        void check();
        const timer = window.setInterval(() => void check(), 300_000);
        window.addEventListener("focus", check);
        document.addEventListener("visibilitychange", check);
        return () => {
            active = false;
            clearInterval(timer);
            window.removeEventListener("focus", check);
            document.removeEventListener("visibilitychange", check);
        };
    }, []);
    if (!available) return null;
    return (
        <div className="fixed inset-x-4 top-3 z-[var(--z-popover)] mx-auto max-w-2xl" role="status">
            <Callout
                tone="info"
                title="影策已有新版本"
                action={
                    <Button size="small" onClick={() => window.location.reload()}>
                        我已保存，刷新页面
                    </Button>
                }
            >
                请先保存当前修改再刷新，避免继续使用旧页面合同。建项草稿按账号保存在本机；页面不会自动刷新。
            </Callout>
        </div>
    );
}
