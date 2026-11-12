import { Button } from "antd";
import { ArrowLeft } from "lucide-react";
import { useNavigate } from "react-router";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { ProjectComposer } from "@/pages/projects/workbench/create/project-composer";

import "./drama-flow.css";

/** 新建短剧：正文和画面类型必填，画风、画幅、单段时长、模型、生成模式都显示并带默认值；建好直接进主页面自动开做。 */
export default function DramaFlowCreatePage() {
    const navigate = useNavigate();
    return (
        <WorkspacePage>
            <div className="drama-flow-create-head">
                <Button type="text" aria-label="返回短剧列表" icon={<ArrowLeft className="size-4" />} onClick={() => navigate("/drama-flow")} />
                <div>
                    <h1>新建短剧</h1>
                    <p>只有正文和画面类型必填，其余都带默认值、都在下面，可以直接改；建好后自动往下做，花钱之前会先问你。</p>
                </div>
            </div>
            <ProjectComposer flow />
        </WorkspacePage>
    );
}
