package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type UserDetails struct {
	ID           uint      `json:"id"`
	UUID         string    `json:"uuid"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Name         string    `json:"name"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	ContactToken string    `json:"contact_token"`
	IsAdmin      bool      `json:"is_admin"`
	IsAutomated  bool      `json:"is_automated"`
	UserType     string    `json:"user_type"`

	// Account details
	EmailVerified    bool `json:"email_verified"`
	TwoFactorEnabled bool `json:"two_factor_enabled"`

	// Assigned capabilities
	Permissions  []string `json:"permissions"`
	Integrations []string `json:"integrations"`

	// Activity details
	LastLogin     *time.Time `json:"last_login,omitempty"`
	SessionsCount int        `json:"sessions_count"`
	ChatsCount    int        `json:"chats_count"`
	MessagesCount int        `json:"messages_count"`
}

type PaginatedUsersData struct {
	database.Pagination
	Users      []UserDetails `json:"users"`
	TotalUsers int64         `json:"total_users"`
}

// userListFilters captures the query parameters accepted by GetUsersWithDetails.
type userListFilters struct {
	Search        string
	Automated     *bool
	Admin         *bool
	EmailVerified *bool
	Integration   string
}

// parseBoolFilter accepts "true"/"false" (and "1"/"0") and treats "all",
// "any" or an empty value as "no filter".
func parseBoolFilter(value string) (*bool, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	switch trimmed {
	case "", "all", "any":
		return nil, nil
	case "true", "1", "yes":
		v := true
		return &v, nil
	case "false", "0", "no":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid boolean filter %q", value)
	}
}

func parseUserListFilters(r *http.Request) (userListFilters, error) {
	query := r.URL.Query()
	filters := userListFilters{
		Search:      strings.TrimSpace(query.Get("search")),
		Integration: strings.ToLower(strings.TrimSpace(query.Get("integration"))),
	}

	// Automated accounts are hidden by default; pass `automated=all` to include
	// them or `automated=true` to list only automated accounts.
	automatedParam := strings.TrimSpace(query.Get("automated"))
	if automatedParam == "" {
		defaultAutomated := false
		filters.Automated = &defaultAutomated
	} else {
		automated, err := parseBoolFilter(automatedParam)
		if err != nil {
			return filters, err
		}
		filters.Automated = automated
	}

	admin, err := parseBoolFilter(query.Get("admin"))
	if err != nil {
		return filters, err
	}
	filters.Admin = admin

	emailVerified, err := parseBoolFilter(query.Get("email_verified"))
	if err != nil {
		return filters, err
	}
	filters.EmailVerified = emailVerified

	return filters, nil
}

// applyUserFilters applies the parsed filters to a user query. The same query
// builder must be used for the count and the paginated fetch so the pagination
// metadata stays consistent.
func applyUserFilters(query *gorm.DB, DB *gorm.DB, filters userListFilters) *gorm.DB {
	if filters.Search != "" {
		like := "%" + strings.ToLower(filters.Search) + "%"
		query = query.Where(
			"LOWER(name) LIKE ? OR LOWER(username) LIKE ? OR LOWER(email) LIKE ?",
			like, like, like,
		)
	}
	if filters.Automated != nil {
		query = query.Where("is_automated = ?", *filters.Automated)
	}
	if filters.Admin != nil {
		query = query.Where("is_admin = ?", *filters.Admin)
	}
	if filters.EmailVerified != nil && DB.Migrator().HasTable("account_states") {
		sub := DB.Table("account_states").Select("user_id").Where("is_email_verified = ?", *filters.EmailVerified)
		query = query.Where("id IN (?)", sub)
	}
	if filters.Integration != "" && DB.Migrator().HasTable("integration_accesses") {
		sub := DB.Table("integration_accesses").Select("user_id").Where("integration_name = ?", filters.Integration)
		query = query.Where("id IN (?)", sub)
	}
	return query
}

func GetUsersWithDetails(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	filters, err := parseUserListFilters(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Setup pagination
	pagination := database.Pagination{Page: 1, Limit: 20}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			if limit > 200 {
				limit = 200
			}
			pagination.Limit = limit
		}
	}

	baseQuery := applyUserFilters(DB.Model(&database.User{}), DB, filters)

	// Get total count for pagination
	var totalUsers int64
	if err := baseQuery.Count(&totalUsers).Error; err != nil {
		http.Error(w, fmt.Sprintf("Error counting users: %v", err), http.StatusInternalServerError)
		return
	}

	pagination.TotalRows = totalUsers
	pagination.TotalPages = int(math.Ceil(float64(totalUsers) / float64(pagination.Limit)))

	// Get users with pagination
	var users []database.User
	if err := baseQuery.
		Offset(pagination.GetOffset()).
		Limit(pagination.GetLimit()).
		Order("created_at DESC").
		Find(&users).Error; err != nil {
		http.Error(w, fmt.Sprintf("Error fetching users: %v", err), http.StatusInternalServerError)
		return
	}

	userDetails := make([]UserDetails, 0, len(users))

	for _, u := range users {
		details := UserDetails{
			ID:               u.ID,
			UUID:             u.UUID,
			CreatedAt:        u.CreatedAt,
			UpdatedAt:        u.UpdatedAt,
			Name:             u.Name,
			Username:         u.Username,
			Email:            u.Email,
			ContactToken:     u.ContactToken,
			IsAdmin:          u.IsAdmin,
			IsAutomated:      u.IsAutomated,
			UserType:         "regular",
			TwoFactorEnabled: u.TwoFactorEnabled,
			Permissions:      []string{},
			Integrations:     []string{},
		}

		if verified, verr := database.IsUserEmailVerified(DB, u.ID); verr == nil {
			details.EmailVerified = verified
		}

		if u.IsAutomated {
			details.UserType = "automated"
		}

		// Assigned integration access.
		if access, aerr := database.ListIntegrationAccessByUserID(DB, u.ID); aerr == nil {
			for _, row := range access {
				details.Integrations = append(details.Integrations, row.IntegrationName)
			}
		}

		// Explicit capability permissions.
		var permissions []database.Permission
		if perr := DB.Where("user_id = ?", u.ID).Order("permission asc").Find(&permissions).Error; perr == nil {
			for _, p := range permissions {
				details.Permissions = append(details.Permissions, string(p.Permission))
			}
		}

		// Get activity stats
		var sessionCount int64
		DB.Model(&database.Session{}).Where("user_id = ?", u.ID).Count(&sessionCount)
		details.SessionsCount = int(sessionCount)

		// Get the latest session for last login
		var latestSession database.Session
		if err := DB.Where("user_id = ?", u.ID).Order("created_at DESC").First(&latestSession).Error; err == nil {
			details.LastLogin = &latestSession.CreatedAt
		}

		// Get chat counts (as participant in User1Id or User2Id)
		var chatCount int64
		DB.Model(&database.Chat{}).
			Where("user1_id = ? OR user2_id = ?", u.ID, u.ID).
			Count(&chatCount)
		details.ChatsCount = int(chatCount)

		// Get message counts
		var messageCount int64
		DB.Model(&database.Message{}).Where("sender_id = ?", u.ID).Count(&messageCount)
		details.MessagesCount = int(messageCount)

		userDetails = append(userDetails, details)
	}

	response := PaginatedUsersData{
		Pagination: pagination,
		Users:      userDetails,
		TotalUsers: totalUsers,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
