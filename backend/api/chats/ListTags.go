package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"sort"
)

// ListTags returns the distinct set of tags/categories used across the
// authenticated user's chats. The frontend uses it to populate the tag filter
// without having to load every chat.
//
//	@Summary      List chat tags
//	@Description  Return the distinct tags used by the authenticated user's chats
//	@Tags         chats
//	@Produce      json
//	@Success      200 {object} map[string]interface{} "Distinct chat tags"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Router       /api/v1/chats/tags [get]
func (h *ChatsHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	rows := []database.Chat{}
	query := DB.Model(&database.Chat{}).Select("tags")
	if !user.IsAdmin {
		query = query.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)
	}
	if err := query.Find(&rows).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	seen := map[string]struct{}{}
	tags := []string{}
	for _, row := range rows {
		for _, tag := range row.Tags {
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"tags": tags})
}
