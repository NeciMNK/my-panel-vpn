package model

// MasterMember is one master panel of a cluster sharing the database. Times
// are database-clock Unix seconds, so masters never compare their own clocks.
type MasterMember struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name     string `json:"name" gorm:"uniqueIndex" example:"master-1"`
	Priority int    `json:"priority" example:"1"`
	Url      string `json:"url" example:"https://panel-1.example.com:2053"`
	UpSince  int64  `json:"upSince" gorm:"column:up_since" example:"1762732800"`
	LastSeen int64  `json:"lastSeen" gorm:"column:last_seen" example:"1762736400"`
}
