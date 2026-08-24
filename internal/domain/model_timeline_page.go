package domain

type TimelinePage struct {
	Events     []IncidentEvent `json:"events"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Total      int             `json:"total"`
}
