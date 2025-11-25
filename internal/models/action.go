package models

import "time"

type Action struct {
	CreatedAt time.Time `json:"created_at"`
	UserId    uint      `json:"user_id"`
	Action    string    `json:"action"`
}
