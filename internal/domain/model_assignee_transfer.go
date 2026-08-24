package domain

import "time"

type AssigneeTransfer struct {
	FromAssignee  string    `json:"from_assignee"`
	ToAssignee    string    `json:"to_assignee"`
	Reason        string    `json:"reason"`
	PreviousDueAt time.Time `json:"previous_due_at"`
	NewDueAt      time.Time `json:"new_due_at"`
	TransferredAt time.Time `json:"transferred_at"`
	Actor         string    `json:"actor"`
}
