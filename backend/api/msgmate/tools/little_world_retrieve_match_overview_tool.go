package tools

import (
	"encoding/json"
	"fmt"
)

type LittleWorldRetrieveMatchOverviewToolInput struct{}

var LittleWorldRetrieveMatchOverviewToolDef = ToolDefinition{
	Name:           "little_world__retrieve_match_overview",
	Description:    "Retrieve an overview of the user's matches in Little World.",
	AdminOnly:      true,
	RequiresInit:   true,
	InputType:      LittleWorldRetrieveMatchOverviewToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(_ interface{}, initData map[string]interface{}) (string, error) {
		sessionID, csrfToken, apiHost, userID, err := extractUserInitData(initData)
		if err != nil {
			return "", err
		}
		fullURL := fmt.Sprintf("%s/api/matching/users/%s/", apiHost, userID)
		responseBody, err := makeAPIRequest("GET", fullURL, nil, sessionID, csrfToken)
		if err != nil {
			return fmt.Sprintf("error fetching user matches: %s", err), err
		}
		matchesJSON, err := extractJSONField(responseBody, "matches")
		if err != nil {
			return "", err
		}
		var matchesData map[string]interface{}
		if err := json.Unmarshal(matchesJSON, &matchesData); err != nil {
			return "", fmt.Errorf("error parsing matches data: %w", err)
		}
		for category, categoryData := range matchesData {
			categoryMap, ok := categoryData.(map[string]interface{})
			if !ok {
				continue
			}
			items, ok := categoryMap["items"].([]interface{})
			if !ok {
				continue
			}
			for i, item := range items {
				match, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				delete(match, "chat")
				items[i] = match
			}
			categoryMap["items"] = items
			matchesData[category] = categoryMap
		}
		cleanedJSON, err := json.Marshal(matchesData)
		if err != nil {
			return "", fmt.Errorf("error converting cleaned matches to JSON: %w", err)
		}
		return string(cleanedJSON), nil
	},
}
