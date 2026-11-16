package model

// SubEdge makes a node serve this panel's subscriptions on its own port: every
// master keeps a tunnel to the node and the node forwards requests through it.
type SubEdge struct {
	Id     int  `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	NodeId int  `json:"nodeId" gorm:"column:node_id;uniqueIndex" example:"2"`
	Enable bool `json:"enable" gorm:"default:true" example:"true"`
	Port   int  `json:"port" gorm:"default:443" example:"443"`
	// CertId is the panel-managed certificate the edge serves; 0 serves plain HTTP.
	CertId int `json:"certId" gorm:"column:cert_id" example:"1"`
	// Status is online while this master's tunnel to the node is up.
	Status    string `json:"status" gorm:"default:unknown" example:"online"`
	LastError string `json:"lastError" gorm:"column:last_error"`
	LastSeen  int64  `json:"lastSeen" gorm:"column:last_seen" example:"1762736400"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt int64  `json:"updatedAt" gorm:"autoUpdateTime"`
}
