package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BriefFact 是需求文档里的一条事实（契约 brief 的 $defs/fact）。
type BriefFact struct {
	Value  string `json:"value"`
	Status string `json:"status"`
	Quote  string `json:"quote,omitempty"`
	Date   string `json:"date,omitempty"`
}

type BriefItem struct {
	ID       string   `json:"id"`
	Content  string   `json:"content"`
	Quote    string   `json:"quote,omitempty"`
	Date     string   `json:"date,omitempty"`
	Segments []string `json:"segments,omitempty"`
	Status   string   `json:"status"`
}

type BriefQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Blocking bool   `json:"blocking"`
	Status   string `json:"status"`
}

// BriefEpisode 是一集的目标（契约 brief 的 episodes[]）。
type BriefEpisode struct {
	No              int     `json:"no"`
	TargetSeconds   float64 `json:"target_seconds,omitempty"`
	HookRequirement string  `json:"hook_requirement,omitempty"`
}

// Brief 是契约 brief：需求文档。
type Brief struct {
	Title      string `json:"title"`
	VideoModel string `json:"video_model"`
	AuthMode   string `json:"auth_mode"`
	// StyleProfile 是手动指定的 yoozee 风格档案；不写时按项目画风对照（style_map.json）。
	StyleProfile string `json:"style_profile,omitempty"`
	// EpisodeSeconds 是每集目标时长（秒，项目设置里填）：生成分镜的提示词和 R-SEG-008 读它。
	EpisodeSeconds *float64        `json:"episode_seconds,omitempty"`
	Episodes       []BriefEpisode  `json:"episodes,omitempty"`
	Purpose        *BriefFact      `json:"purpose,omitempty"`
	Platform       *BriefFact      `json:"platform,omitempty"`
	Duration       *BriefFact      `json:"duration,omitempty"`
	Taste          *BriefFact      `json:"taste,omitempty"`
	Budget         *BriefFact      `json:"budget,omitempty"`
	Acceptance     *BriefFact      `json:"acceptance,omitempty"`
	Items          []BriefItem     `json:"items,omitempty"`
	NotDoing       []string        `json:"not_doing,omitempty"`
	Constraints    []string        `json:"constraints,omitempty"`
	Questions      []BriefQuestion `json:"questions,omitempty"`
	DecidedForYou  []string        `json:"decided_for_you,omitempty"`
}

// BriefStatuses 是事实可用的状态；只有「已确认」「默认，已授权」能拿去花钱。
var BriefStatuses = []string{"已确认", "默认，待确认", "默认，已授权", "待答"}

// BriefShape 是写给 Agent 和人看的字段说明，出错时一并返回。
const BriefShape = "需求文档字段：title；video_model；auth_mode（step/full）；purpose、platform、duration、taste、budget、acceptance 各是 {value, status, quote?, date?}，status 只能是 已确认 / 默认，待确认 / 默认，已授权 / 待答；items 是 [{id: R-001, content, status, quote?, date?, segments?}]；questions 是 [{id: Q-001, question, blocking, status}]；not_doing、constraints、decided_for_you 是字符串数组；episode_seconds 是每集目标秒数（数字）；episodes 是 [{no, target_seconds?, hook_requirement?}]；style_profile 是 yoozee 风格档案 ID（不写按项目画风对照）。画幅、平台写进 platform，片长写进 duration。"

var (
	briefItemID     = regexp.MustCompile(`^R-[0-9]{3}$`)
	briefQuestionID = regexp.MustCompile(`^Q-[0-9]{3}$`)
)

// ParseBrief 按契约解析需求文档；不认识的字段、形状不对的事实都会报出来，方便写的一方改正。
func ParseBrief(raw []byte) (Brief, error) {
	var brief Brief
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&brief); err != nil {
		return Brief{}, fmt.Errorf("需求文档不符合契约 brief：%s（字段写法见工具说明）", briefDecodeHint(err))
	}
	var problems []string
	facts := []struct {
		key  string
		fact *BriefFact
	}{{"purpose", brief.Purpose}, {"platform", brief.Platform}, {"duration", brief.Duration}, {"taste", brief.Taste}, {"budget", brief.Budget}, {"acceptance", brief.Acceptance}}
	for _, item := range facts {
		if item.fact == nil {
			continue
		}
		if strings.TrimSpace(item.fact.Value) == "" {
			problems = append(problems, item.key+".value 不能为空")
		}
		if !containsString(BriefStatuses, item.fact.Status) {
			problems = append(problems, fmt.Sprintf("%s.status 只能是 %s", item.key, strings.Join(BriefStatuses, " / ")))
		}
	}
	if brief.AuthMode != "" && brief.AuthMode != AuthStep && brief.AuthMode != AuthFull {
		problems = append(problems, "auth_mode 只能是 step 或 full")
	}
	if s := brief.EpisodeSeconds; s != nil && (*s < 5 || *s > 3600) {
		problems = append(problems, "episode_seconds 是每集秒数，应在 5 到 3600 之间")
	}
	for _, ep := range brief.Episodes {
		if ep.No < 1 || ep.TargetSeconds < 0 {
			problems = append(problems, "episodes 的 no 从 1 起，target_seconds 不能是负数")
			break
		}
	}
	if id := strings.TrimSpace(brief.StyleProfile); id != "" {
		if _, err := BuiltinProfile(id); err != nil {
			problems = append(problems, fmt.Sprintf("style_profile 只能是 %s", strings.Join(BuiltinProfileIDs(), " / ")))
		}
	}
	for _, item := range brief.Items {
		if !briefItemID.MatchString(item.ID) || strings.TrimSpace(item.Content) == "" {
			problems = append(problems, "items 的 id 要写成 R-001 这样并带 content")
			break
		}
	}
	for _, question := range brief.Questions {
		if !briefQuestionID.MatchString(question.ID) || strings.TrimSpace(question.Question) == "" {
			problems = append(problems, "questions 的 id 要写成 Q-001 这样并带 question")
			break
		}
	}
	if len(problems) > 0 {
		if len(problems) > 3 {
			problems = append(problems[:3], "……")
		}
		return Brief{}, fmt.Errorf("需求文档不符合契约 brief：%s（字段写法见工具说明）", strings.Join(problems, "；"))
	}
	return brief, nil
}

func briefDecodeHint(err error) string {
	message := err.Error()
	if strings.HasPrefix(message, "json: unknown field ") {
		return "不认识的字段 " + strings.TrimPrefix(message, "json: unknown field ")
	}
	if strings.Contains(message, "cannot unmarshal string into Go struct field") && strings.Contains(message, "BriefFact") {
		return "事实字段要写成 {value, status} 对象，不能是一句字符串"
	}
	return message
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

var briefFactFields = []string{"purpose", "platform", "duration", "taste", "budget", "acceptance"}

var briefKnownFields = map[string]bool{"title": true, "video_model": true, "auth_mode": true, "style_profile": true, "episode_seconds": true, "episodes": true, "purpose": true, "platform": true, "duration": true, "taste": true, "budget": true, "acceptance": true, "items": true, "not_doing": true, "constraints": true, "questions": true, "decided_for_you": true}

// NormalizeBrief 把早期写进来的不合契约的需求文档整理成契约形状（读的时候用，不丢内容）：
// 事实写成字符串的改成 {value, status: 默认，待确认}；问题、要求写成字符串的补上编号；
// 不认识的字段原样挪进 constraints，留给人和 Agent 重新归位。
func NormalizeBrief(doc map[string]any) map[string]any {
	for _, key := range briefFactFields {
		if value, ok := doc[key].(string); ok {
			doc[key] = map[string]any{"value": value, "status": "默认，待确认"}
		}
	}
	if list, ok := doc["questions"].([]any); ok {
		for i, raw := range list {
			if text, ok := raw.(string); ok {
				list[i] = map[string]any{"id": fmt.Sprintf("Q-%03d", i+1), "question": text, "blocking": false, "status": "待答"}
			}
		}
	}
	if list, ok := doc["items"].([]any); ok {
		for i, raw := range list {
			if text, ok := raw.(string); ok {
				list[i] = map[string]any{"id": fmt.Sprintf("R-%03d", i+1), "content": text, "status": "待处理"}
			}
		}
	}
	var moved []string
	for key, value := range doc {
		if briefKnownFields[key] {
			continue
		}
		encoded, _ := json.Marshal(value)
		moved = append(moved, fmt.Sprintf("%s：%s", key, strings.Trim(string(encoded), `"`)))
		delete(doc, key)
	}
	if len(moved) > 0 {
		sort.Strings(moved)
		constraints, _ := doc["constraints"].([]any)
		for _, item := range moved {
			constraints = append(constraints, item)
		}
		doc["constraints"] = constraints
	}
	return doc
}

var briefDurationPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(分钟|分|秒|s|min)`)

// EpisodeTargetSeconds 是第 episode 集的目标片长（秒），0 表示没设：episodes[].target_seconds 优先，
// 其次每集时长 episode_seconds，最后从 duration.value 读第一个「数字 + 单位」（check.py 的 R-SEG-008 同序）。
func (b Brief) EpisodeTargetSeconds(episode int) float64 {
	for _, ep := range b.Episodes {
		if ep.No == episode && ep.TargetSeconds > 0 {
			return ep.TargetSeconds
		}
	}
	if b.EpisodeSeconds != nil && *b.EpisodeSeconds > 0 {
		return *b.EpisodeSeconds
	}
	if b.Duration != nil {
		if m := briefDurationPattern.FindStringSubmatch(b.Duration.Value); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			if m[2] == "分钟" || m[2] == "分" || m[2] == "min" {
				v *= 60
			}
			return v
		}
	}
	return 0
}
