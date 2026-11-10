package model

// WarpSwitch is one server's WARP switch: off, all traffic, or only the listed
// sites (domains, geosite: and regexp: entries). Node id 0 is this panel.
type WarpSwitch struct {
	Id        int      `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	NodeId    int      `json:"nodeId" gorm:"column:node_id;uniqueIndex" example:"0"`
	Mode      string   `json:"mode" gorm:"default:off" example:"domains"`
	Domains   []string `json:"domains" gorm:"serializer:json"`
	LastError string   `json:"lastError" gorm:"column:last_error"`
	UpdatedAt int64    `json:"updatedAt" gorm:"autoUpdateTime"`
}
