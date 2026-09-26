package entitlements

import (
	"encoding/json"
	"fmt"
)

func Boolean(values map[string]json.RawMessage, name string) (bool, error) {
	raw, ok := values[name]
	if !ok {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("entitlement %s is not boolean: %w", name, err)
	}
	return value, nil
}
func Limit(values map[string]json.RawMessage, name string) (int64, bool, error) {
	raw, ok := values[name]
	if !ok {
		return 0, false, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
		return 0, true, fmt.Errorf("entitlement %s is not a non-negative integer", name)
	}
	return value, true, nil
}
