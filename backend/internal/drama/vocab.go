package drama

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"

	builtinAssets "yingce/backend/builtin"
)

// 词库（与 common.vocab 一致）：内置知识包里的 JSONL，一条一行，保持文件顺序。
// 表情核心词匹配按文件顺序取第一个最高分，顺序不能丢。

// ArcPhase 是情绪弧的一段：秒区间、标签与可见信号。
type ArcPhase struct {
	Label   string   `json:"label"`
	From    float64  `json:"from"`
	To      float64  `json:"to"`
	Signals []string `json:"signals"`
	Inner   string   `json:"inner"`
}

// EmotionArc 是 kb-performance vocab/emotion-arcs.jsonl 的一条。
type EmotionArc struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Family string     `json:"family"`
	Phases []ArcPhase `json:"phases"`
}

// Expression 是 kb-performance vocab/expressions.jsonl 的一条。
type Expression struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Family  string   `json:"family"`
	ZhTerms []string `json:"zh_terms"`
}

// ActionPattern 是 kb-performance vocab/action-patterns.jsonl 的一条。
type ActionPattern struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Parent    string   `json:"parent"`
	Process   []string `json:"process"`
	Variants  []string `json:"variants"`
	Roles     []string `json:"roles"`
	Forbid    []string `json:"forbid"`
	TwoPerson bool     `json:"two_person"`
}

// ActionEntry 是 kb-action 的特效（vfx.jsonl）或武学（martial.jsonl）条目。
type ActionEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Definition string `json:"定义"`
	AIExec     string `json:"AI技术执行"`
}

// Vocab 是装段、检查用到的全部词库。
type Vocab struct {
	Arcs        []EmotionArc
	Expressions []Expression
	Actions     []ActionPattern
	Vfx         []ActionEntry
	Martial     []ActionEntry

	arcByID     map[string]*EmotionArc
	actionByID  map[string]*ActionPattern
	vfxByID     map[string]*ActionEntry
	martialByID map[string]*ActionEntry
}

const (
	vocabArcsPath        = "kb-performance/vocab/emotion-arcs.jsonl"
	vocabExpressionsPath = "kb-performance/vocab/expressions.jsonl"
	vocabActionsPath     = "kb-performance/vocab/action-patterns.jsonl"
	vocabVfxPath         = "kb-action/vocab/vfx.jsonl"
	vocabMartialPath     = "kb-action/vocab/martial.jsonl"
)

var (
	vocabOnce sync.Once
	vocabData *Vocab
	vocabErr  error
)

func readJSONL[T any](filesystem fs.FS, path string) ([]T, error) {
	raw, err := fs.ReadFile(filesystem, path)
	if err != nil {
		return nil, fmt.Errorf("读取词库 %s 失败: %w", path, err)
	}
	var out []T
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for n := 1; scanner.Scan(); n++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("词库 %s 第 %d 行格式错误: %w", path, n, err)
		}
		out = append(out, item)
	}
	return out, scanner.Err()
}

// LoadVocab 从任意文件系统读取词库（测试用）。
func LoadVocab(filesystem fs.FS) (*Vocab, error) {
	v := &Vocab{}
	var err error
	if v.Arcs, err = readJSONL[EmotionArc](filesystem, vocabArcsPath); err != nil {
		return nil, err
	}
	if v.Expressions, err = readJSONL[Expression](filesystem, vocabExpressionsPath); err != nil {
		return nil, err
	}
	if v.Actions, err = readJSONL[ActionPattern](filesystem, vocabActionsPath); err != nil {
		return nil, err
	}
	if v.Vfx, err = readJSONL[ActionEntry](filesystem, vocabVfxPath); err != nil {
		return nil, err
	}
	if v.Martial, err = readJSONL[ActionEntry](filesystem, vocabMartialPath); err != nil {
		return nil, err
	}
	v.arcByID = map[string]*EmotionArc{}
	for i := range v.Arcs {
		v.arcByID[v.Arcs[i].ID] = &v.Arcs[i]
	}
	v.actionByID = map[string]*ActionPattern{}
	for i := range v.Actions {
		v.actionByID[v.Actions[i].ID] = &v.Actions[i]
	}
	v.vfxByID = map[string]*ActionEntry{}
	for i := range v.Vfx {
		v.vfxByID[v.Vfx[i].ID] = &v.Vfx[i]
	}
	v.martialByID = map[string]*ActionEntry{}
	for i := range v.Martial {
		v.martialByID[v.Martial[i].ID] = &v.Martial[i]
	}
	return v, nil
}

// BuiltinVocab 返回内置知识包的词库（只读，进程内缓存）。
func BuiltinVocab() (*Vocab, error) {
	vocabOnce.Do(func() {
		vocabData, vocabErr = LoadVocab(builtinAssets.FS)
	})
	return vocabData, vocabErr
}

func (v *Vocab) Arc(id string) *EmotionArc           { return v.arcByID[id] }
func (v *Vocab) Action(id string) *ActionPattern     { return v.actionByID[id] }
func (v *Vocab) VfxEntry(id string) *ActionEntry     { return v.vfxByID[id] }
func (v *Vocab) MartialEntry(id string) *ActionEntry { return v.martialByID[id] }
