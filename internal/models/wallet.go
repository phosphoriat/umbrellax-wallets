package models

import "time"

type Wallet struct {
	CreatedAt time.Time `json:"created_at"`
	UserId    uint      `json:"user_id" gorm:"primaryKey"`
	Network   string    `json:"network" gorm:"primaryKey"`
	Address   string    `json:"address"`
}

func (Wallet) TableName() string {
	return "wallets"
}
