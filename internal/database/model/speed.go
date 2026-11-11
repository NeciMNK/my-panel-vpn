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

// SpeedTestResult is one measurement of a server's own internet link; a
// failed run keeps its Error and no speeds.
type SpeedTestResult struct {
	Id           int     `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	NodeId       int     `json:"nodeId" gorm:"column:node_id;index" example:"0"`
	CreatedAt    int64   `json:"createdAt" gorm:"index" example:"1762874430000"`
	Trigger      string  `json:"trigger" example:"manual"`
	Provider     string  `json:"provider" example:"speedtest"`
	Server       string  `json:"server" example:"Rostelecom, Moscow"`
	PingMs       float64 `json:"pingMs" example:"4.2"`
	DownloadMbps float64 `json:"downloadMbps" example:"930.5"`
	UploadMbps   float64 `json:"uploadMbps" example:"910.1"`
	UsedMbps     float64 `json:"usedMbps" example:"35.2"`
	CapacityMbps int     `json:"capacityMbps" example:"946"`
	Error        string  `json:"error" example:""`
}
