package database

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupPaginationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	DB := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "pagination_test.db"),
		ResetDB:  true,
	})
	return DB
}

func createPaginationTestUser(t *testing.T, DB *gorm.DB, username string) *User {
	t.Helper()
	user := User{Username: username, Email: username, ContactToken: username + "-token"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user %q: %v", username, err)
	}
	return &user
}

// Paginate used to divide by the raw pagination.Limit, so a caller that left
// the limit at zero divided by zero (TotalPages became a garbage value) and a
// negative limit produced a negative page count. GetLimit normalises both to
// the default page size, and Paginate must divide by the same normalised value
// (ci#747).
func TestPaginateNormalisesNonPositiveLimit(t *testing.T) {
	DB := setupPaginationTestDB(t)
	owner := createPaginationTestUser(t, DB, "owner@pagination.example")
	for i := 0; i < 3; i++ {
		partner := createPaginationTestUser(t, DB, "partner-"+string(rune('a'+i))+"@pagination.example")
		if err := DB.Create(&Contact{OwningUserId: owner.ID, ContactUserId: partner.ID}).Error; err != nil {
			t.Fatalf("failed to create contact: %v", err)
		}
	}

	for _, limit := range []int{0, -5} {
		pagination := Pagination{Page: 1, Limit: limit}
		var contacts []Contact
		query := DB.Where("owning_user_id = ?", owner.ID).
			Where("deleted_at IS NULL")
		if err := query.Scopes(Paginate(&contacts, &pagination, query)).Find(&contacts).Error; err != nil {
			t.Fatalf("limit %d: query failed: %v", limit, err)
		}
		if pagination.Limit != 10 {
			t.Fatalf("limit %d: pagination.Limit = %d, want the default 10", limit, pagination.Limit)
		}
		if pagination.TotalPages != 1 {
			t.Fatalf("limit %d: TotalPages = %d, want 1 (3 rows at the default page size)", limit, pagination.TotalPages)
		}
		if pagination.GetOffset() != 0 {
			t.Fatalf("limit %d: offset = %d, want 0 (a negative limit must not produce a negative offset)", limit, pagination.GetOffset())
		}
		if len(contacts) != 3 {
			t.Fatalf("limit %d: got %d contacts, want 3", limit, len(contacts))
		}
	}
}

// Paginate counts the rows of the handle it is given. When a caller chained its
// filters *after* Scopes(...) the count ran against the unscoped handle, so
// TotalPages described every row in the table instead of the caller's rows
// (ci#747). The filtered query must be passed in - the shape backend/api/chats
// already uses - and the count must follow it.
func TestPaginateCountsOnlyTheScopedRows(t *testing.T) {
	DB := setupPaginationTestDB(t)
	owner := createPaginationTestUser(t, DB, "scoped-owner@pagination.example")
	other := createPaginationTestUser(t, DB, "scoped-other@pagination.example")

	for i := 0; i < 12; i++ {
		partner := createPaginationTestUser(t, DB, "scoped-partner-"+string(rune('a'+i))+"@pagination.example")
		if err := DB.Create(&Contact{OwningUserId: owner.ID, ContactUserId: partner.ID}).Error; err != nil {
			t.Fatalf("failed to create owner contact: %v", err)
		}
	}
	otherUser := createPaginationTestUser(t, DB, "scoped-stranger@pagination.example")
	if err := DB.Create(&Contact{OwningUserId: other.ID, ContactUserId: otherUser.ID}).Error; err != nil {
		t.Fatalf("failed to create other contact: %v", err)
	}

	// 13 contacts exist in total, 12 of them owned by `owner`.
	pagination := Pagination{Page: 1, Limit: 10}
	var contacts []Contact
	query := DB.Where("owning_user_id = ?", owner.ID).Where("deleted_at IS NULL")
	if err := query.Scopes(Paginate(&contacts, &pagination, query)).Find(&contacts).Error; err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if pagination.TotalRows != 12 {
		t.Fatalf("TotalRows = %d, want 12 (the caller's rows only)", pagination.TotalRows)
	}
	// 12 rows at limit 10 -> exactly 2 pages. Counting all 13 rows would yield
	// the same 2 here, so the assertion that catches the regression is the row
	// count and TotalRows; keep TotalPages pinned too for completeness.
	if pagination.TotalPages != 2 {
		t.Fatalf("TotalPages = %d, want 2", pagination.TotalPages)
	}
	if len(contacts) != 10 {
		t.Fatalf("got %d contacts on page 1, want the full page of 10", len(contacts))
	}

	// The unfiltered handle must still see every row, so the scoping is what
	// changed the count - not the fixture.
	allPages := Pagination{Page: 1, Limit: 10}
	var all []Contact
	if err := DB.Scopes(Paginate(&all, &allPages, DB)).Find(&all).Error; err != nil {
		t.Fatalf("unscoped query failed: %v", err)
	}
	if allPages.TotalRows != 13 {
		t.Fatalf("unscoped TotalRows = %d, want 13", allPages.TotalRows)
	}
	if allPages.TotalPages != 2 {
		t.Fatalf("unscoped TotalPages = %d, want 2 (13 rows at limit 10)", allPages.TotalPages)
	}
}
