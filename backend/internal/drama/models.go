// Package drama 是短剧制作线（yoozee 1.0 skills）在后端的领域规则：
// 可选生成模型及其边界、中性分段契约、方言编译、产物依赖哈希、进度推导和成片量数。
//
// 模型边界的唯一来源是内置技能 kb-dialects 里的 models.json；技能包（本机运行）与后端读同一份文件，
// 方言编译规则与 yoozee-skills/bindings/local/tools/compile.py 一致，并由两边共用的样例做对照测试。
package drama

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	builtinAssets "yingce/backend/builtin"
)

const modelsPath = "kb-dialects/models.json"

// DurationLimit 是一次生成（一段）允许的时长范围，单位秒。
type DurationLimit struct {
	Min  int `json:"min"`
	Max  int `json:"max"`
	Step int `json:"step"`
}

// RefLimits 是一次生成可挂的参考素材上限；nil 表示文档未写、尚未校准。
type RefLimits struct {
	Images *int `json:"images"`
	Videos *int `json:"videos"`
	Audios *int `json:"audios"`
	Total  *int `json:"total"`
}

// Model 是一个可选的视频生成模型族（不是渠道里的具体模型名）。
type Model struct {
	ID                      string             `json:"id"`
	Label                   string             `json:"label"`
	Vendor                  string             `json:"vendor"`
	Kind                    string             `json:"kind"`
	Match                   []string           `json:"match"`
	Duration                DurationLimit      `json:"duration"`
	Refs                    RefLimits          `json:"refs"`
	FramesExclusiveWithRefs *bool              `json:"frames_exclusive_with_refs"`
	PromptMaxChars          *int               `json:"prompt_max_chars"`
	Resolutions             []string           `json:"resolutions"`
	FPS                     *int               `json:"fps"`
	AudioInVideo            bool               `json:"audio_in_video"`
	PricePerSecUSD          map[string]float64 `json:"price_per_sec_usd"`
	Checked                 string             `json:"checked"`
	Calibrated              bool               `json:"calibrated"`
	Sources                 []string           `json:"sources"`
}

// MaxDurationMs 是一段的最长时长（毫秒）。
func (m Model) MaxDurationMs() int64 { return int64(m.Duration.Max) * 1000 }

// Dialect 是提示词方言族：seedance 或 minimax-h3。
func (m Model) Dialect() string {
	if strings.HasPrefix(m.ID, "seedance") {
		return "seedance"
	}
	return "minimax-h3"
}

// Registry 是 models.json 的内容。
type Registry struct {
	Version string  `json:"version"`
	Note    string  `json:"note"`
	Default string  `json:"default"`
	Models  []Model `json:"models"`
}

var (
	registryOnce sync.Once
	registry     Registry
	registryErr  error
	blocksOnce   sync.Once
	blocks       map[string]string
	blocksErr    error
)

// LoadRegistry 从任意文件系统读取 models.json（测试用）。
func LoadRegistry(filesystem fs.FS, path string) (Registry, error) {
	raw, err := fs.ReadFile(filesystem, path)
	if err != nil {
		return Registry{}, fmt.Errorf("读取生成模型目录失败: %w", err)
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return Registry{}, fmt.Errorf("生成模型目录格式错误: %w", err)
	}
	if len(reg.Models) == 0 {
		return Registry{}, fmt.Errorf("生成模型目录为空")
	}
	seen := map[string]bool{}
	for _, m := range reg.Models {
		if m.ID == "" || seen[m.ID] {
			return Registry{}, fmt.Errorf("生成模型 ID 为空或重复: %q", m.ID)
		}
		seen[m.ID] = true
		if m.Duration.Min <= 0 || m.Duration.Max < m.Duration.Min {
			return Registry{}, fmt.Errorf("生成模型 %s 的时长范围无效", m.ID)
		}
	}
	if !seen[reg.Default] {
		return Registry{}, fmt.Errorf("默认生成模型 %q 不在目录里", reg.Default)
	}
	return reg, nil
}

// Models 返回内置的生成模型目录（只读，进程内缓存）。
func Models() (Registry, error) {
	registryOnce.Do(func() {
		registry, registryErr = LoadRegistry(builtinAssets.FS, modelsPath)
	})
	return registry, registryErr
}

// ModelByID 按 ID 查模型。
func ModelByID(id string) (Model, bool) {
	reg, err := Models()
	if err != nil {
		return Model{}, false
	}
	id = strings.TrimSpace(id)
	for _, m := range reg.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// ResolveModel 返回项目使用的模型：空 ID 用默认模型；未知 ID 返回错误。
func ResolveModel(id string) (Model, error) {
	reg, err := Models()
	if err != nil {
		return Model{}, err
	}
	if strings.TrimSpace(id) == "" {
		id = reg.Default
	}
	m, ok := ModelByID(id)
	if !ok {
		return Model{}, fmt.Errorf("不支持的生成模型：%s", id)
	}
	return m, nil
}

// MatchChannelModel 判断渠道里的具体模型名属于哪个模型族（按 match 片段，忽略大小写）。
// 先比较更长的片段，避免 "seedance-2" 抢走 "seedance-2-5"。
func MatchChannelModel(name string) (Model, bool) {
	reg, err := Models()
	if err != nil {
		return Model{}, false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return Model{}, false
	}
	best, bestLen := Model{}, 0
	for _, m := range reg.Models {
		for _, frag := range m.Match {
			frag = strings.ToLower(frag)
			if len(frag) > bestLen && strings.Contains(lower, frag) {
				best, bestLen = m, len(frag)
			}
		}
	}
	return best, bestLen > 0
}

// Blocks 返回方言编译用的风格块和表演块（kb-dialects/blocks）。
func Blocks(dialect string) (style string, acting string, err error) {
	blocksOnce.Do(func() {
		blocks = map[string]string{}
		for _, name := range []string{"video_style.txt", "acting.txt", "video_style.zh.txt", "acting.zh.txt"} {
			raw, readErr := fs.ReadFile(builtinAssets.FS, "kb-dialects/blocks/"+name)
			if readErr != nil {
				blocksErr = fmt.Errorf("读取方言风格块失败: %w", readErr)
				return
			}
			blocks[name] = strings.TrimSpace(string(raw))
		}
	})
	if blocksErr != nil {
		return "", "", blocksErr
	}
	if dialect == "seedance" {
		return blocks["video_style.zh.txt"], blocks["acting.zh.txt"], nil
	}
	return blocks["video_style.txt"], blocks["acting.txt"], nil
}
