package cloudlink

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
	"github.com/goccy/go-json"
)

func Filter(source map[snowflake.ID]*Client, exclusions ...*Client) map[snowflake.ID]*Client {
	filtered := make(map[snowflake.ID]*Client)
	for id, client := range source {
		found := false
		for _, exclusion := range exclusions {
			if client.ID == exclusion.ID {
				found = true
				break
			}
		}
		if !found {
			filtered[id] = client
		}
	}
	return filtered
}

func isTypeDeclaration(val any) bool {
	if valMap, ok := val.(map[string]any); ok {
		if cmd, ok := valMap["cmd"].(string); ok && cmd == "type" {
			return true
		}
	}
	return false
}

// Helper to validate username/room name types
func validateUsername(name any) error {
	switch name.(type) {
	case string, int64, float64, bool:
		return nil // Valid types
	default:
		return fmt.Errorf("username/room value must be a string, boolean, float, or int")
	}
}

// Helper to convert single value or slice/array to []any
func ToSlice(value any) ([]any, error) {
	switch v := value.(type) {
	case nil:
		return []any{}, nil // Empty slice for nil
	case []any:
		return v, nil // Already a slice
	case string, int64, float64, bool:
		return []any{v}, nil // Single element slice
	default:
		// Try unmarshalling if it's a string that might be a JSON array
		if strVal, ok := v.(string); ok {
			var sliceVal []any
			if err := json.Unmarshal([]byte(strVal), &sliceVal); err == nil {
				return sliceVal, nil
			}
		}
		return nil, fmt.Errorf("value is not a slice, array, or single valid element")
	}
}
