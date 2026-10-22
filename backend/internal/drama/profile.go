package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sync"

	builtinAssets "yingce/backend/builtin"
)

// 风格档案与规则表（yz-producer 里的 profiles/、rules/，与 common.profile / common.rules 一致）。

const (
	profilesDir = "yz-producer/profiles"
	rulesDir    = "yz-producer/rules"
	// DefaultProfileID 是没有选档案时的默认档案（common.project_profile）。
	DefaultProfileID = "live-realism"
)

// LangBlock 是档案里的中英文风格块或表演块。
type LangBlock struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// SpacePolicy 是一种空间类型的景别与运镜禁令。
type SpacePolicy struct {
	MaxSize    string   `json:"max_size"`
	No         []string `json:"no"`
	NoBirdseye bool     `json:"no_birdseye"`
}

// ProfileQuotas 是档案配额；nil 表示档案没写这一项。
type ProfileQuotas struct {
	NormalMax              *float64    `json:"normal_max"`
	Side45Min              *float64    `json:"side45_min"`
	Side45Max              *float64    `json:"side45_max"`
	Side90Max              *float64    `json:"side90_max"`
	FixedMax               *float64    `json:"fixed_max"`
	CloseupMax             *float64    `json:"closeup_max"`
	NonPersonMin           *float64    `json:"non_person_min"`
	NonPersonMax           *float64    `json:"non_person_max"`
	GroupWideMin           *float64    `json:"group_wide_min"`
	ConsecutiveSameMoveMax *int        `json:"consecutive_same_move_max"`
	ConsecutiveSameSizeMax *int        `json:"consecutive_same_size_max"`
	DutchMax               *int        `json:"dutch_max"`
	ExtremeLowMax          *int        `json:"extreme_low_max"`
	ExtremeHighMax         *int        `json:"extreme_high_max"`
	ShakeViolentMax        *int        `json:"shake_violent_max"`
	MinAngles              *int        `json:"min_angles"`
	MinMoves               *int        `json:"min_moves"`
	HeavyMoves             orderedInts `json:"heavy_moves"`
}

// ProfileRules 是档案规则；nil 表示没写，用规则的默认值。
type ProfileRules struct {
	OneTakeAllowed            *bool    `json:"one_take_allowed"`
	StandingTalkMaxSec        *float64 `json:"standing_talk_max_sec"`
	MaxMoveAxes               *int     `json:"max_move_axes"`
	NoRotationAtZ1Z3          *bool    `json:"no_rotation_at_z1_z3"`
	AdjacentSizeMinGap        *int     `json:"adjacent_size_min_gap"`
	IndoorLightAnchorRequired *bool    `json:"indoor_light_anchor_required"`
	ExpressionMinSec          *float64 `json:"expression_min_sec"`
	DialogueOpenSize          []string `json:"dialogue_open_size"`
	GroupVariationRequired    bool     `json:"group_variation_required"`
}

// Profile 是一个风格档案（只取检查、装段、编译会读的字段）。
type Profile struct {
	ID                   string                 `json:"id"`
	Label                string                 `json:"label"`
	StyleBlock           LangBlock              `json:"style_block"`
	ActingBlock          LangBlock              `json:"acting_block"`
	PerformanceGrain     string                 `json:"performance_grain"`
	EmotionArcRequired   bool                   `json:"emotion_arc_required"`
	CameraFollowsEmotion bool                   `json:"camera_follows_emotion"`
	LightPolicy          string                 `json:"light_policy"`
	Quotas               ProfileQuotas          `json:"quotas"`
	Rules                ProfileRules           `json:"rules"`
	SpacePolicy          map[string]SpacePolicy `json:"space_policy"`
	VfxPolicy            struct {
		IPPack string `json:"ip_pack"`
	} `json:"vfx_policy"`
}

func (r ProfileRules) maxMoveAxes() int {
	if r.MaxMoveAxes != nil {
		return *r.MaxMoveAxes
	}
	return 2
}

func (r ProfileRules) standingTalkMaxSec() float64 {
	if r.StandingTalkMaxSec != nil {
		return *r.StandingTalkMaxSec
	}
	return 3
}

func (r ProfileRules) adjacentSizeMinGap() int {
	if r.AdjacentSizeMinGap != nil {
		return *r.AdjacentSizeMinGap
	}
	return 2
}

func (r ProfileRules) expressionMinSec() float64 {
	if r.ExpressionMinSec != nil {
		return *r.ExpressionMinSec
	}
	return 0.5
}

func boolOr(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

// orderedInts 是保留键顺序的 {名称: 次数}（heavy_moves 的报错顺序跟档案写的顺序走）。
type orderedInts []struct {
	Key   string
	Value int
}

func (o *orderedInts) UnmarshalJSON(raw []byte) error {
	obj, err := parseOrderedObject(raw)
	if err != nil {
		return err
	}
	*o = (*o)[:0]
	for _, kv := range obj {
		var n int
		if err := json.Unmarshal(kv.value, &n); err != nil {
			return err
		}
		*o = append(*o, struct {
			Key   string
			Value int
		}{kv.key, n})
	}
	return nil
}

type orderedField struct {
	key   string
	value json.RawMessage
}

func parseOrderedObject(raw []byte) ([]orderedField, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("应为 JSON 对象")
	}
	var out []orderedField
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		key := keyTok.(string)
		replaced := false
		for i := range out {
			if out[i].key == key {
				out[i].value, replaced = value, true
			}
		}
		if !replaced {
			out = append(out, orderedField{key, value})
		}
	}
	return out, nil
}

func encodeOrderedObject(fields []orderedField) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(f.key)
		b.Write(key)
		b.WriteByte(':')
		b.Write(f.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// 子档案逐键合并这几项，其余字段整体覆盖（common.profile）。
var profileMergedKeys = map[string]bool{"quotas": true, "rules": true, "color": true, "lens_policy": true, "space_policy": true, "vfx_policy": true, "asset_tier_policy": true}

var profileIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func loadProfileRaw(filesystem fs.FS, id string, depth int) ([]orderedField, error) {
	if !profileIDPattern.MatchString(id) {
		return nil, fmt.Errorf("风格档案 %q 无效", id)
	}
	if depth > 4 {
		return nil, fmt.Errorf("风格档案 %s 的 extends 层数过多", id)
	}
	raw, err := fs.ReadFile(filesystem, profilesDir+"/"+id+".json")
	if err != nil {
		return nil, fmt.Errorf("风格档案 %s 不存在", id)
	}
	fields, err := parseOrderedObject(raw)
	if err != nil {
		return nil, fmt.Errorf("风格档案 %s 格式错误: %w", id, err)
	}
	var parent string
	for _, f := range fields {
		if f.key == "extends" {
			_ = json.Unmarshal(f.value, &parent)
		}
	}
	if parent == "" {
		return fields, nil
	}
	merged, err := loadProfileRaw(filesystem, parent, depth+1)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		idx := -1
		for i := range merged {
			if merged[i].key == f.key {
				idx = i
			}
		}
		value := f.value
		if idx >= 0 && profileMergedKeys[f.key] {
			base, errBase := parseOrderedObject(merged[idx].value)
			child, errChild := parseOrderedObject(f.value)
			if errBase == nil && errChild == nil {
				for _, c := range child {
					found := false
					for i := range base {
						if base[i].key == c.key {
							base[i].value, found = c.value, true
						}
					}
					if !found {
						base = append(base, c)
					}
				}
				value = encodeOrderedObject(base)
			}
		}
		if idx >= 0 {
			merged[idx].value = value
		} else {
			merged = append(merged, orderedField{f.key, value})
		}
	}
	return merged, nil
}

// LoadProfile 从任意文件系统读档案并处理 extends（测试用）。
func LoadProfile(filesystem fs.FS, id string) (*Profile, error) {
	fields, err := loadProfileRaw(filesystem, id, 0)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(encodeOrderedObject(fields), &p); err != nil {
		return nil, fmt.Errorf("风格档案 %s 格式错误: %w", id, err)
	}
	return &p, nil
}

var (
	profileCacheMu sync.Mutex
	profileCache   = map[string]*Profile{}
)

// BuiltinProfile 返回内置档案（进程内缓存）。
func BuiltinProfile(id string) (*Profile, error) {
	profileCacheMu.Lock()
	defer profileCacheMu.Unlock()
	if p, ok := profileCache[id]; ok {
		return p, nil
	}
	p, err := LoadProfile(builtinAssets.FS, id)
	if err != nil {
		return nil, err
	}
	profileCache[id] = p
	return p, nil
}

// RuleLevels 是规则表里每条规则的级别（error / warn）。
type RuleLevels map[string]string

// LoadRuleLevels 读 rules/<kind>.json 的规则级别。
func LoadRuleLevels(filesystem fs.FS, kind string) (RuleLevels, error) {
	raw, err := fs.ReadFile(filesystem, rulesDir+"/"+kind+".json")
	if err != nil {
		return nil, fmt.Errorf("读取规则表 %s 失败: %w", kind, err)
	}
	var rules []struct {
		ID    string `json:"id"`
		Level string `json:"level"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("规则表 %s 格式错误: %w", kind, err)
	}
	out := RuleLevels{}
	for _, r := range rules {
		out[r.ID] = r.Level
	}
	return out, nil
}

var (
	shotRulesOnce sync.Once
	shotRules     RuleLevels
	shotRulesErr  error
)

// BuiltinShotRules 返回镜头表规则级别（进程内缓存）。
func BuiltinShotRules() (RuleLevels, error) {
	shotRulesOnce.Do(func() {
		shotRules, shotRulesErr = LoadRuleLevels(builtinAssets.FS, "shots")
	})
	return shotRules, shotRulesErr
}
