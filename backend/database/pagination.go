package database

import (
	"math"

	"gorm.io/gorm"
)

type Pagination struct {
	Limit      int         `json:"limit,omitempty" query:"limit"`
	Page       int         `json:"page,omitempty" query:"page"`
	Sort       string      `json:"sort,omitempty" query:"sort"`
	TotalRows  int64       `json:"-"`
	TotalPages int         `json:"total_pages"`
	Rows       interface{} `json:"rows"`
}

func (p *Pagination) GetOffset() int {
	return (p.GetPage() - 1) * p.GetLimit()
}

func (p *Pagination) GetLimit() int {
	// A non-positive limit is not usable: 0 and negative values both used to
	// survive here (and a raw p.Limit was divided by in Paginate), which made
	// TotalPages collapse or go negative and pushed a negative OFFSET into the
	// query (ci#747). Clamp both to the default page size.
	if p.Limit <= 0 {
		p.Limit = 10
	}
	return p.Limit
}

func (p *Pagination) GetPage() int {
	if p.Page == 0 {
		p.Page = 1
	}
	return p.Page
}

func (p *Pagination) GetSort() string {
	if p.Sort == "" {
		p.Sort = "Id desc"
	}
	return p.Sort
}

func Paginate(value interface{}, pagination *Pagination, db *gorm.DB) func(db *gorm.DB) *gorm.DB {
	var totalRows int64
	db.Model(value).Count(&totalRows)

	pagination.TotalRows = totalRows
	// Divide by GetLimit, not by a raw pagination.Limit: a caller that left the
	// limit at zero divided by zero here (TotalPages collapsed to a huge int)
	// and a negative limit produced a negative page count (ci#747). GetLimit
	// normalises the value in place, so the response and the OFFSET below agree.
	limit := pagination.GetLimit()
	totalPages := int(math.Ceil(float64(totalRows) / float64(limit)))
	pagination.TotalPages = totalPages

	return func(db *gorm.DB) *gorm.DB {
		return db.Offset(pagination.GetOffset()).Limit(pagination.GetLimit()).Order(pagination.GetSort())
	}
}
