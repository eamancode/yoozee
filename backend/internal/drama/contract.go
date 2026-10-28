package drama

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// 步骤的输出契约：contracts/ 下带版本号的 JSON Schema。契约文字（提示词里的【受保护输出契约】）由 Text 按 schema 生成，
// 不再在前端手写；字段顺序取 required 数组。schema 里柚子自己的关键字：
//   - x-contract：契约文字的固定句子（标题、格式要求、rows 引导句、默认要求、补充规则）；layout 为 "lines" 时
//     模型一镜一行输出（JSON Lines），顶层 rows 只描述逻辑形状；layout 为 "blocks" 时一项一块（剧本优化一集一块：
//     一行 JSON 头加若干行正文）；items 是逐项数组在顶层的字段名（默认 rows）；
//   - x-shape: "skill"：字段形状由技能正文说明（如 spec），单独成行，要求每镜都写；
//   - x-section: "assets"：字段说明跟在资产清单那段之后（清单为空时不出现），由 FieldInstruction 取。

//go:embed contracts/*.schema.json
var contractFS embed.FS

// contractFiles 是契约版本 → schema 文件。
var contractFiles = map[string]string{
	ContractCanvasStoryboard: "contracts/chapter.shots.v2.schema.json",
	ContractChapterShots:     "contracts/chapter.shots.v3.schema.json",
	ContractScriptAdapt:      "contracts/script.adapt.v1.schema.json",
	ContractStyleBible:       "contracts/style.bible.v1.schema.json",
	ContractStyleLock:        "contracts/style.lock.v1.schema.json",
	ContractAssetsExtract:    "contracts/assets.extract.v1.schema.json",
}

// ContractCanvasStoryboard 是画布拆镜的输出契约：整段一个 {"title", "rows"} JSON 对象（前端解析），不随生成分段升级。
const ContractCanvasStoryboard = "chapter.shots@2"

// contractLayoutLines 是一镜一行的契约（x-contract.layout）；contractLayoutBlocks 是一项一块（JSON 头加正文）。
const (
	contractLayoutLines  = "lines"
	contractLayoutBlocks = "blocks"
)

type contractProse struct {
	Heading string `json:"heading"`
	Layout  string `json:"layout"`
	Items   string `json:"items"`
	// Unit 是逐行契约里一行是什么（「个镜头」「个资产」），默认「个镜头」。
	Unit        string   `json:"unit"`
	Format      string   `json:"format"`
	RowsIntro   string   `json:"rowsIntro"`
	Requirement string   `json:"requirement"`
	Rules       []string `json:"rules"`
}

type contractNode struct {
	ID          string                   `json:"$id"`
	Title       string                   `json:"title"`
	Description string                   `json:"description"`
	Type        string                   `json:"type"`
	Required    []string                 `json:"required"`
	Properties  map[string]*contractNode `json:"properties"`
	Items       *contractNode            `json:"items"`
	MinItems    *int                     `json:"minItems"`
	MaxItems    *int                     `json:"maxItems"`
	Minimum     *int                     `json:"minimum"`
	Maximum     *int                     `json:"maximum"`
	Prose       *contractProse           `json:"x-contract"`
	Shape       string                   `json:"x-shape"`
	Section     string                   `json:"x-section"`
	// OneOf 是几种可接受的写法；契约文字按第一种写（首选写法），其余是解析容错。
	OneOf []*contractNode `json:"oneOf"`
}

// preferred 是契约文字用的形状：没写 type、写了 oneOf 的字段取第一种写法，说明用字段自己的。
func (n *contractNode) preferred() *contractNode {
	if n.Type != "" || len(n.OneOf) == 0 {
		return n
	}
	p := *n.OneOf[0]
	if n.Description != "" {
		p.Description = n.Description
	}
	return &p
}

// Contract 是一个步骤输出契约（schema 原文与解析结果）。
type Contract struct {
	ID    string
	Title string
	raw   []byte
	root  *contractNode
	// itemsKey 是顶层逐项数组的字段名（rows、episodes）；rows 是数组的元素。
	itemsKey string
	rows     *contractNode
	fields   []string
}

var (
	contractMu    sync.Mutex
	contractCache = map[string]*Contract{}
)

// BuiltinContract 读一个契约版本；schema 写坏时返回错误（启动后第一次用到就会暴露，测试里也会读一遍）。
func BuiltinContract(id string) (*Contract, error) {
	contractMu.Lock()
	defer contractMu.Unlock()
	if c, ok := contractCache[id]; ok {
		return c, nil
	}
	file, ok := contractFiles[id]
	if !ok {
		return nil, fmt.Errorf("未知的输出契约 %s", id)
	}
	raw, err := contractFS.ReadFile(file)
	if err != nil {
		return nil, err
	}
	c, err := parseContract(raw)
	if err != nil {
		return nil, fmt.Errorf("输出契约 %s：%w", id, err)
	}
	if c.ID != id {
		return nil, fmt.Errorf("输出契约 %s 的 $id 写成了 %s", id, c.ID)
	}
	contractCache[id] = c
	return c, nil
}

func parseContract(raw []byte) (*Contract, error) {
	var root contractNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("schema 不是合法 JSON：%w", err)
	}
	if root.Prose == nil || root.Prose.Heading == "" || root.Prose.Format == "" || root.Prose.RowsIntro == "" {
		return nil, fmt.Errorf("schema 缺少 x-contract 的固定句子")
	}
	itemsKey := root.Prose.Items
	if itemsKey == "" {
		itemsKey = "rows"
	}
	rows := root.Properties[itemsKey]
	if root.Type != "object" || rows == nil || rows.Type != "array" || rows.Items == nil || rows.Items.Type != "object" {
		return nil, fmt.Errorf("schema 顶层必须是 {%s: object[]}", itemsKey)
	}
	for _, name := range root.Required {
		if root.Properties[name] == nil {
			return nil, fmt.Errorf("顶层 required 里的 %s 没有定义", name)
		}
	}
	fields := slices.Clone(rows.Items.Required)
	for _, name := range fields {
		if rows.Items.Properties[name] == nil {
			return nil, fmt.Errorf("%s 的 required 里的 %s 没有定义", itemsKey, name)
		}
	}
	// 不在 required 里的字段按名字排在后面，契约里同样列出。
	var extra []string
	for name := range rows.Items.Properties {
		if !slices.Contains(fields, name) {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	return &Contract{ID: root.ID, Title: root.Title, raw: raw, root: &root, itemsKey: itemsKey, rows: rows.Items, fields: append(fields, extra...)}, nil
}

// Lines 表示模型一镜一行输出（JSON Lines），不是整段 JSON 对象。
func (c *Contract) Lines() bool { return c.root.Prose.Layout == contractLayoutLines }

// Blocks 表示模型一项一块输出（一行 JSON 头加若干行正文），不是整段 JSON 对象。
func (c *Contract) Blocks() bool { return c.root.Prose.Layout == contractLayoutBlocks }

// Schema 是契约的 JSON Schema 原文。
func (c *Contract) Schema() []byte { return slices.Clone(c.raw) }

// RowFields 是镜头行的全部字段（契约顺序）。
func (c *Contract) RowFields() []string { return slices.Clone(c.fields) }

// DefaultRequirement 是不带镜头数、每镜时长时的要求句。
func (c *Contract) DefaultRequirement() string { return c.root.Prose.Requirement }

// ContractTextOptions 控制契约文字里出现哪些字段。
type ContractTextOptions struct {
	// SkillShaped 为 false 时不写形状由技能说明的字段（画布拆镜没有技能，只要柚子分镜行字段）。
	SkillShaped bool
}

// Text 生成【受保护输出契约】：requirement 跟在「rows 至少 1 项。」之后；x-section 字段不在这里写。
func (c *Contract) Text(requirement string, opts ContractTextOptions) string {
	prose := c.root.Prose
	var list, shaped []string
	for _, name := range c.fields {
		field := c.rows.Properties[name]
		switch {
		case field.Section != "":
		case field.Shape == "skill":
			if opts.SkillShaped {
				shaped = append(shaped, c.FieldInstruction(name))
			}
		default:
			list = append(list, fieldLabel(name, field.preferred()))
		}
	}
	shape := fmt.Sprintf("- 顶层固定为 %s，%s。%s", c.topLevelShape(), c.rowsMinText(), requirement)
	switch {
	case c.Lines():
		unit := prose.Unit
		if unit == "" {
			unit = "个镜头"
		}
		shape = "- 至少输出 1 " + unit + "。" + requirement
	case c.Blocks():
		shape = "- 至少输出 1 项。" + requirement
	}
	lines := []string{prose.Heading, "- " + prose.Format, shape, "- " + prose.RowsIntro}
	lines = append(lines, wrapFieldList(list)...)
	lines = append(lines, shaped...)
	for _, rule := range prose.Rules {
		lines = append(lines, "- "+rule)
	}
	return strings.Join(lines, "\n")
}

// FieldInstruction 是单独成行的字段要求（「- rows 每项再加 …」）；字段不存在时返回空串。
func (c *Contract) FieldInstruction(name string) string {
	field := c.rows.Properties[name]
	if field == nil {
		return ""
	}
	label := typeLabel(field.preferred())
	if field.Shape == "skill" {
		label += "，形状见技能说明，每个镜头都要写，不能省略"
	}
	prefix := "rows 每项"
	if c.Lines() {
		prefix = "每个镜头"
	}
	line := fmt.Sprintf("- %s再加 %s（%s）", prefix, name, label)
	if field.Description != "" {
		line += "：" + field.Description
	}
	return line
}

// topLevelShape 是 {"title": string, "rows": object[]} 这样的顶层形状。
func (c *Contract) topLevelShape() string {
	parts := make([]string, 0, len(c.root.Required))
	for _, name := range c.root.Required {
		parts = append(parts, fmt.Sprintf("%q: %s", name, jsonTypeName(c.root.Properties[name])))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (c *Contract) rowsMinText() string {
	rows := c.root.Properties[c.itemsKey]
	if rows.MinItems != nil && *rows.MinItems > 0 {
		return fmt.Sprintf("%s 至少 %d 项", c.itemsKey, *rows.MinItems)
	}
	return c.itemsKey + " 可以为空"
}

func jsonTypeName(n *contractNode) string {
	switch n.Type {
	case "array":
		if n.Items != nil {
			return jsonTypeName(n.Items) + "[]"
		}
		return "array"
	case "integer":
		return "number"
	default:
		return n.Type
	}
}

func typeLabel(n *contractNode) string {
	switch n.Type {
	case "integer":
		return "整数"
	case "number":
		return "数字"
	case "string":
		return "字符串"
	case "boolean":
		return "布尔值"
	case "object":
		return "对象"
	case "array":
		if n.Items != nil && n.Items.Type != "" && n.Items.Type != "array" {
			return typeLabel(n.Items) + "数组"
		}
		return "数组"
	}
	return n.Type
}

// fieldLabel 是字段清单里的一项：字符串字段只写说明，其他类型写「类型，说明或取值范围」。
func fieldLabel(name string, n *contractNode) string {
	if n.Type == "string" {
		if n.Description == "" {
			return name
		}
		return name + "（" + n.Description + "）"
	}
	parts := []string{typeLabel(n)}
	switch {
	case n.Description != "":
		parts = append(parts, n.Description)
	case n.Minimum != nil && n.Maximum != nil:
		parts = append(parts, strconv.Itoa(*n.Minimum)+"-"+strconv.Itoa(*n.Maximum))
	}
	if n.MaxItems != nil {
		parts = append(parts, fmt.Sprintf("最多 %d 项", *n.MaxItems))
	}
	return name + "（" + strings.Join(parts, "，") + "）"
}

// contractLineChars 是字段清单折行的大致宽度（字符数）。
const contractLineChars = 60

// wrapFieldList 把字段清单按「、」接起来、两格缩进折行，最后一项后面加句号。
func wrapFieldList(items []string) []string {
	var lines []string
	var current strings.Builder
	for i, item := range items {
		sep := "、"
		if i == len(items)-1 {
			sep = "。"
		}
		current.WriteString(item + sep)
		if utf8.RuneCountInString(current.String()) >= contractLineChars || i == len(items)-1 {
			lines = append(lines, "  "+current.String())
			current.Reset()
		}
	}
	return lines
}

// ContractRequirementPlaceholder 是前端契约模板里要求句的占位符。
const ContractRequirementPlaceholder = "{{requirement}}"

// CanvasContractTS 生成前端画布拆镜用的契约模板文件（web/src/lib/canvas/storyboard-output-contract.generated.ts）：
// 画布拆镜没有绑定技能，不写形状由技能说明的字段，也没有资产清单那段的字段。
func CanvasContractTS(c *Contract) string {
	text := c.Text(ContractRequirementPlaceholder, ContractTextOptions{})
	escaped := strings.NewReplacer(`\`, `\\`, "`", "\\`", "${", `\${`).Replace(text)
	return "// 由 backend/internal/drama/" + contractFiles[c.ID] + " 生成，不要手改。\n" +
		"// 改契约后重新生成：cd backend && go test ./internal/drama -run TestCanvasStoryboardContractGenerated -update\n\n" +
		"export const STORYBOARD_CONTRACT_ID = \"" + c.ID + "\";\n\n" +
		"export const STORYBOARD_CONTRACT_REQUIREMENT = \"" + ContractRequirementPlaceholder + "\";\n\n" +
		"export const STORYBOARD_CONTRACT_TEMPLATE = `" + escaped + "`;\n"
}
