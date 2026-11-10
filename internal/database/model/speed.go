package model

// ClientSpeed is a user's speed contract: MaxMbps caps them (0 = no cap),
// MinMbps is the speed a server must still have free for them (0 = any).
type ClientSpeed struct {
	Id      int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Email   string `json:"email" gorm:"uniqueIndex" example:"anna"`
	MinMbps int    `json:"minMbps" gorm:"column:min_mbps" example:"20"`
	MaxMbps int    `json:"maxMbps" gorm:"column:max_mbps" example:"100"`
}

// ServerCapacity is the link speed of one server (node id 0 = this panel),
// which the free speed per user is measured against.
type ServerCapacity struct {
	Id           int `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	NodeId       int `json:"nodeId" gorm:"column:node_id;uniqueIndex" example:"0"`
	CapacityMbps int `json:"capacityMbps" gorm:"column:capacity_mbps" example:"1000"`
}
