package model

// Cascade chains an entry server to one or more exit servers: users connect to
// the entry, their traffic leaves the internet from an exit. Node id 0 means
// this panel itself.
type Cascade struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name        string `json:"name" example:"RU → DE"`
	Enable      bool   `json:"enable" gorm:"default:true" example:"true"`
	EntryNodeId int    `json:"entryNodeId" gorm:"column:entry_node_id" example:"1"`
	ExitNodeIds []int  `json:"exitNodeIds" gorm:"serializer:json;column:exit_node_ids"`
	// ExitCountry, when set, makes every enabled node of that country an exit.
	ExitCountry      string   `json:"exitCountry" gorm:"column:exit_country" example:"DE"`
	EntryInboundTags []string `json:"entryInboundTags" gorm:"serializer:json;column:entry_inbound_tags"`
	Strategy         string   `json:"strategy" gorm:"default:leastPing" example:"leastPing"`
	// Mode is split, relay or both; empty is a cascade saved before modes, a relay.
	Mode             string        `json:"mode" example:"split"`
	DirectDomains    []string      `json:"directDomains" gorm:"serializer:json;column:direct_domains"`
	DirectIPs        []string      `json:"directIps" gorm:"serializer:json;column:direct_ips"`
	RelayInboundTags []string      `json:"relayInboundTags" gorm:"serializer:json;column:relay_inbound_tags"`
	Links            []CascadeLink `json:"links" gorm:"serializer:json"`
	LastError        string        `json:"lastError" gorm:"column:last_error"`
	CreatedAt        int64         `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt        int64         `json:"updatedAt" gorm:"autoUpdateTime"`
}

// CascadeLink is the hidden VLESS+REALITY hop the master provisions on one exit.
type CascadeLink struct {
	NodeId     int    `json:"nodeId"`
	InboundId  int    `json:"inboundId"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	ClientId   string `json:"clientId"`
	Email      string `json:"email"`
	PublicKey  string `json:"publicKey"`
	ShortId    string `json:"shortId"`
	ServerName string `json:"serverName"`
}

// CascadePin keeps one user on one exit of a multi-exit cascade, so a reconnect
// within the hold window leaves the internet from the same IP.
type CascadePin struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	CascadeId int    `json:"cascadeId" gorm:"column:cascade_id;uniqueIndex:idx_cascade_pin_user"`
	Email     string `json:"email" gorm:"uniqueIndex:idx_cascade_pin_user"`
	NodeId    int    `json:"nodeId" gorm:"column:node_id"`
	LastSeen  int64  `json:"lastSeen" gorm:"column:last_seen"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime"`
}

// CascadeFixedExit is an admin's permanent choice of exit for one user: every
// cascade with that exit sends the user there while it is up.
type CascadeFixedExit struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Email     string `json:"email" gorm:"uniqueIndex:idx_cascade_fixed_exit" example:"anna"`
	NodeId    int    `json:"nodeId" gorm:"column:node_id;uniqueIndex:idx_cascade_fixed_exit" example:"2"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime" example:"1762862400"`
}

// CascadeAICountry is the one country a user uses AI services from: their pin
// there carries AI domains only, or all their traffic when AllTraffic is set.
type CascadeAICountry struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Email      string `json:"email" gorm:"uniqueIndex"`
	Country    string `json:"country"`
	AllTraffic bool   `json:"allTraffic" gorm:"column:all_traffic;default:false"`
}
