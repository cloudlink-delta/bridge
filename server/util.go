package cloudlink

import "github.com/bwmarrin/snowflake"

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
