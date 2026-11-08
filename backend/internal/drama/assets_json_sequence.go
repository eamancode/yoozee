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
