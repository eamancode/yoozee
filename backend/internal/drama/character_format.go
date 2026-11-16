package drama

import "regexp"

// Only explicit frame annotations are rewritten; character proportions and
// numeric appearance details in existing asset definitions remain untouched.
var fourPanelFrameAnnotation = regexp.MustCompile(`(?i)(?:landscape|portrait|aspect\s*ratio\s*[:：]?|画幅(?:比例)?\s*[:：]?|画面比例\s*[:：]?|竖版|横版|横屏|竖屏)\s*[0-9]+\s*[:：]\s*[0-9]+`)

func FourPanelPortraitPrompt(prompt string) string {
	return fourPanelFrameAnnotation.ReplaceAllString(prompt, "竖版9:16")
}

// AssetFramePrompt 把资产设定、项目画风里写死的画幅标注（landscape 16:9、竖屏9:16、画幅比例 4:3……）换成这张资产图自己的画幅。
// 项目视频画幅不适用于资产设定图：空间母版固定方形，道具本体图不继承项目横竖屏。frame 为空时直接去掉标注。
// 和 FourPanelPortraitPrompt 同一条规则，只改明确的画幅标注，不动外观里的数字和比例描述。
func AssetFramePrompt(prompt string, frame string) string {
	return fourPanelFrameAnnotation.ReplaceAllString(prompt, frame)
}

// SpatialMasterOutputNote 接在空间母版提示词最后：输出画幅和空间结构以母版规格为准（前面设定、画风里的冲突写法不适用）。
const SpatialMasterOutputNote = "【空间母版输出规格】整张图片固定为方形1:1，45～60度高斜俯视的完整空间总览；项目视频画幅、资产设定和画风里出现的其他画幅与横竖方向要求不适用于本张母版。按设定的空间类型保留真实结构：围合室内只移开屋顶，四面墙完整保留，不拆墙、不剖切、不做透明墙；半室外保留真实顶棚、立柱和敞开边界；外景不虚构围墙和屋顶。设定里写一件的设施只画一件，不复制、不增减主要设施。不出现人物、文字、标签、水印。"

// PropBodyOutputNote 是道具本体图提示词最后的独立性要求：只画这一件道具。name 是道具名。
func PropBodyOutputNote(name string) string {
	return "【道具本体输出规格】画面只呈现「" + name + "」这一件道具本体（含它自身的固定部件），置于简洁中性的背景上；资产设定里为交代剧情写到的其他物件、人物、食物、容器内容物和场景陈设只作情境说明，不画进本张图。数量、湿度、磨损等状态按设定写；材质与颜色遵循项目画风和主基调色卡。不出现文字、标签、水印。"
}
