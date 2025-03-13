package queue

import "strings"

func IsAutomatedUser(userName string) bool {
	botNames := []string{"signal", "bot", "msgmate"}
	normalizedUserName := strings.ToLower(strings.TrimSpace(userName))

	for _, botName := range botNames {
		if normalizedUserName == botName {
			return true
		}
	}

	return false
}
