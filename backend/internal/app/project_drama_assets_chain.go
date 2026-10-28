// 场次色卡写完后自动按集提取资产（M8 卡 D）：色卡在前、资产在后。场次色卡收尾成功后，对这次色卡覆盖的剧集按集序取第一集建提取任务
// （metadata.chainedFrom = 上一个任务，metadata.dramaAssets.chain 记起这一串的色卡任务和后面还要提取的集），这一集提取收尾成功后
// 再接下一集——一次只跑一集，免得几集同时按名字和别名合并同一个角色。用同一个文本模型和配置；场次色卡带了资产出图模板时
// 各集提取都给新资产出图。用户在「生成场次色卡」对话框里可以关掉（chainAssets=false）。提取失败或取消时后面的集不再接。

package app

import (
	"encoding/json"
	"slices"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaChainInput 是接着建任务要沿用的上一个任务的模型配置和资产出图模板（从解密后的任务输入读）。
type dramaChainInput struct {
	Config         map[string]any          `json:"config"`
	AssetImageTask *DramaImageTaskTemplate `json:"assetImageTask"`
}

func (s *Service) dramaChainInputOf(task model.Task) (dramaChainInput, error) {
	var input dramaChainInput
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return input, err
	}
	_ = json.Unmarshal([]byte(decrypted), &input)
	if !input.AssetImageTask.usable() {
		input.AssetImageTask = nil
	}
	return input, nil
}

// chainAssetsAfterScenePalette 在场次色卡写完后接着给这次覆盖的第一集建提取任务；没开接续时返回 nil。
func (s *Service) chainAssetsAfterScenePalette(task model.Task, projectID string) *DramaChainedTask {
	run := taskDramaScenePalette(task.InputJSON)
	if run == nil || !run.ChainAssets {
		return nil
	}
	episodes := slices.Clone(run.Episodes)
	slices.SortStableFunc(episodes, func(a, b dramaScenePaletteEpisodeMeta) int { return a.No - b.No })
	queue := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		queue = append(queue, ep.UnitID)
	}
	return s.chainNextAssetsExtract(task, projectID, task.ID, queue)
}

// chainAfterAssetsExtract 在一集提取写完后接着提取这一串里的下一集；不是自动接着跑的、或已经是最后一集时返回 nil。
func (s *Service) chainAfterAssetsExtract(task model.Task, projectID string, am *dramaAssetsMeta) *DramaChainedTask {
	if am == nil || am.Chain == nil || len(am.Chain.Queue) == 0 {
		return nil
	}
	return s.chainNextAssetsExtract(task, projectID, am.Chain.From, am.Chain.Queue)
}

// chainNextAssetsExtract 按顺序给 queue 里第一个能提取的单元建提取任务（还没有场次色卡、单元已删的跳过，原因记日志），
// 剩下的记进新任务的 chain.queue。一个都建不成时返回最后一个原因。
func (s *Service) chainNextAssetsExtract(task model.Task, projectID string, from string, queue []string) *DramaChainedTask {
	chained := &DramaChainedTask{Step: drama.StepAssetsExtract}
	input, err := s.dramaChainInputOf(task)
	if err != nil {
		chained.Error = err.Error()
		return chained
	}
	for i, unitID := range queue {
		req := DramaStepRunRequest{Model: task.Model, LogicalModelID: task.LogicalModelID, Config: input.Config, ImageTask: input.AssetImageTask, TraceID: task.TraceID,
			chainedFrom: task.ID, chain: &dramaAssetChain{From: from, Queue: slices.Clone(queue[i+1:])}}
		result, err := s.RunDramaStep(task.UserID, projectID, unitID, drama.StepAssetsExtract, req)
		if err != nil {
			chained.Error = userFacingTaskError(err.Error())
			_ = s.log(task.UserID, task.ID, "warn", "没有接着提取资产", chained.Error)
			continue
		}
		chained.TaskID, chained.UnitID, chained.Error = result.Task.ID, unitID, ""
		chained.Remaining = len(queue) - i - 1
		return chained
	}
	return chained
}
