import { Checkbox } from "antd";
import { CircleHelp } from "lucide-react";
import { useState } from "react";

import { IconButton } from "@/components/ui/base/buttons";
import { AntTooltip } from "@/components/ui/base/tooltip/ant-tooltip";

const WHITE_MODEL_HELP = "开启后，同场景连续镜头自动使用上一组片末白模作为站位参考；参考不可用时仍可继续生成视频。";
const SETTINGS_HELP = "修改后对新提交的生成请求生效，已开始的生成链沿用提交时设置。";

/** 建项和项目设置共用的白模选项；说明按钮位于 checkbox label 外，避免误切换。 */
export function WhiteModelStoryboardOption({ checked, onChange, settings = false }: { checked: boolean; onChange: (checked: boolean) => void; settings?: boolean }) {
    const [helpOpen, setHelpOpen] = useState(false);
    return (
        <div className="inline-flex items-center gap-1.5" data-slot="white-model-storyboard-option">
            <Checkbox className="[&_.ant-checkbox-label]:pe-0" checked={checked} onChange={(event) => onChange(event.target.checked)}>
                白模分镜
            </Checkbox>
            <AntTooltip title={WHITE_MODEL_HELP + (settings ? SETTINGS_HELP : "")} trigger={["hover", "focus", "click"]} placement="topLeft" open={helpOpen} onOpenChange={setHelpOpen} styles={{ root: { maxWidth: "min(360px, calc(100vw - 32px))" } }}>
                <IconButton
                    icon={CircleHelp}
                    size="xs"
                    aria-label="白模分镜说明"
                    className="focus-visible:ring-2 focus-visible:ring-ring"
                    onClick={() => setHelpOpen(true)}
                    onKeyDown={(event) => {
                        if (event.key === "Escape") {
                            event.preventDefault();
                            event.stopPropagation();
                            setHelpOpen(false);
                        }
                    }}
                />
            </AntTooltip>
        </div>
    );
}
