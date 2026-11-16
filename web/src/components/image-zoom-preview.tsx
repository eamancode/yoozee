import { useEffect } from "react";
import { Image as AntImage } from "antd";

/**
 * 单张图片的放大预览（antd 图片预览：滚轮 / 按钮缩放、拖动、旋转，Esc 或关闭按钮退出，按原图比例显示）。
 * 挂上即打开，关掉时 onClose。由调用方用一个可聚焦的按钮触发，键盘也能打开。
 * 预览开着时 Esc 只关预览：在捕获阶段把这次 Esc 标成已处理（preventDefault），外层（如详情栏）见到 defaultPrevented 就不跟着关。
 */
export function ImageZoomPreview({ src, alt, onClose }: { src: string; alt: string; onClose: () => void }) {
    useEffect(() => {
        const claimEscape = (event: KeyboardEvent) => {
            if (event.key === "Escape") event.preventDefault();
        };
        window.addEventListener("keydown", claimEscape, true);
        return () => window.removeEventListener("keydown", claimEscape, true);
    }, []);
    return (
        <AntImage
            src={src}
            alt={alt}
            style={{ display: "none" }}
            preview={{
                open: true,
                movable: true,
                minScale: 0.5,
                maxScale: 12,
                scaleStep: 0.25,
                onOpenChange: (open) => {
                    if (!open) onClose();
                },
            }}
        />
    );
}
