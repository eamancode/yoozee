package drama

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Decode complete adjacent objects without editing their fields or accepting a broken tail.
func assetJSONObjects(line string) ([]map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(line))
	var objects []map[string]json.RawMessage
	for {
		var object map[string]json.RawMessage
		err := decoder.Decode(&object)
		if err == io.EOF {
			return objects, nil
		}
		if err != nil {
			return nil, err
		}
		if object == nil {
			return nil, errors.New("资产必须为JSON对象")
		}
		objects = append(objects, object)
	}
}

// AssetLineObjects 读一行模型输出里的资产对象，解析（AssetsExtractFromText）和校验（ValidateIntegratedAssetsText）共用这一个入口：
//   - 以 { 开头：一个或几个紧挨着的完整对象（assetJSONObjects，坏尾整行不合格，规则不变）；
//   - 说明文字在前、对象在后（「我会按技能提取资产。{"name":…}」）：从第一个对象起点（assetObjectStart：{ 后面跳过 JSON 空白紧跟 "，
//     紧凑的 {" 和带空格、制表符的 { " 都算）起按同一规则完整读出，不再整行跳过——线上曾因此静默丢掉写在说明文字同一行的主角；
//   - 纯说明文字、代码块围栏：matched=false，跳过；
//   - 像对象却读不全：matched=true 并返回错误，调用方据此记一行读不出（原契约）或整轮不合格（资产一体化契约）。
//
// 按 JSON 解码器逐个读，不按花括号切分，字符串里的括号、引号、转义不会被误切。
func AssetLineObjects(raw string) (objects []map[string]json.RawMessage, matched bool, err error) {
	line := strings.TrimSuffix(jsTrim(raw), ",")
	start := 0
	if !strings.HasPrefix(line, "{") {
		if start = assetObjectStart(line); start < 0 {
			return nil, false, nil
		}
	}
	objects, err = assetJSONObjects(line[start:])
	return objects, true, err
}

// assetObjectStart 是说明文字后第一个 JSON 对象的起点：一个 { 之后跳过 JSON 允许的空白（空格、制表、回车、换行）紧跟 "。
// 只认紧凑的 {" 会漏掉 { "林深"… 这种同样合法的写法，整行当成说明静默跳过。取最靠前的起点，嵌套对象的起点总在外层之后。
// { 和 " 都是单字节 ASCII，按字节扫不会落在多字节字符中间。没有对象起点（纯说明、「{占位}」这类）返回 -1。
func assetObjectStart(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != '{' {
			continue
		}
		j := i + 1
		for j < len(line) && strings.IndexByte(" \t\r\n", line[j]) >= 0 {
			j++
		}
		if j < len(line) && line[j] == '"' {
			return i
		}
	}
	return -1
}
