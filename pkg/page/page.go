package page

const (
	DefaultPageNum  = 1
	DefaultPageSize = 20
	MaxPageSize     = 200
)

type Request struct {
	PageNum  int `json:"pageNum"`
	PageSize int `json:"pageSize"`
}

func (p *Request) Normalize() {
	if p.PageNum <= 0 {
		p.PageNum = DefaultPageNum
	}
	if p.PageSize <= 0 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
}

func (p *Request) Offset() int {
	return (p.PageNum - 1) * p.PageSize
}

func (p *Request) Limit() int {
	return p.PageSize
}

// page.Response[handler.TenantDTO]{Total: total, List: items}
type Response[T any] struct {
	Total int64 `json:"total"`
	List  []T   `json:"list"`
}
