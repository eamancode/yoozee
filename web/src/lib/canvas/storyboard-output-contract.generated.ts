// 由 backend/internal/drama/contracts/chapter.shots.v2.schema.json 生成，不要手改。
// 改契约后重新生成：cd backend && go test ./internal/drama -run TestCanvasStoryboardContractGenerated -update

export const STORYBOARD_CONTRACT_ID = "chapter.shots@2";

export const STORYBOARD_CONTRACT_REQUIREMENT = "{{requirement}}";

export const STORYBOARD_CONTRACT_TEMPLATE = `【受保护输出契约】
- 只返回一个 JSON 对象，不要 Markdown、表格、代码块、注释或任何解释文字。
- 顶层固定为 {"title": string, "rows": object[]}，rows 至少 1 项。{{requirement}}
- rows 每项对应一个镜头，按剧情顺序排列；字段缺失时填空字符串或空数组，不要省略字段：
  shotNumber（整数，从 1 递增）、durationSeconds（整数，1-60）、plotDescription（本镜可见的剧情与关键动作）、
  dialogue（本镜实际念出的台词或简短旁白，超长必须拆镜）、characters（字符串数组，本镜出现的角色名）、narrativeIntent、
  viewerPOV、performanceBlocking、shotSize（景别）、emotion、lightingAndAtmosphere、
  audioEffects、camera、motion、timeBeats、imageGenerationPrompt（首帧画面提示词）、
  videoMotionPrompt（运镜与结尾状态）、continuityOut、negativePrompt、mustHave（字符串数组，最多 3 项）、
  optionalDetails（字符串数组）。
- 镜头数量以完整覆盖剧情因果、人物关系和关键台词为准，不要为压缩数量删减关键情节。`;
