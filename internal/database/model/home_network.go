package model

// HomeNetwork is a network behind a tunnel (WireGuard, OpenVPN or L2TP/IPsec)
// that one server holds; users reach only the subnets and IPs their rules allow.
type HomeNetwork struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name          string `json:"name" example:"Home"`
	Enable        bool   `json:"enable" gorm:"default:true" example:"true"`
	Kind          string `json:"kind" example:"wireguard"`
	GatewayNodeId int    `json:"gatewayNodeId" gorm:"column:gateway_node_id" example:"0"`
	// Config is the pasted wg-quick or .ovpn file.
	Config   string `json:"config"`
	Server   string `json:"server" example:"203.0.113.7"`
	Username string `json:"username"`
	Password string `json:"password"`
	Psk      string `json:"psk"`
	// Subnets and Hosts are every address of the home network the panel routes.
	Subnets []string          `json:"subnets" gorm:"serializer:json"`
	Hosts   []string          `json:"hosts" gorm:"serializer:json"`
	Rules   []HomeAccessRule  `json:"rules" gorm:"serializer:json"`
	Link    *CascadeLink      `json:"link" gorm:"serializer:json"`
	Servers []int             `json:"servers" gorm:"serializer:json"`
	Errors  map[string]string `json:"errors" gorm:"serializer:json"`
	Applied string            `json:"-" gorm:"column:applied"`
	// MasterAddress is how other servers reach this panel when it is the gateway.
	MasterAddress string `json:"-" gorm:"column:master_address"`
	TunnelState   string `json:"tunnelState" gorm:"column:tunnel_state" example:"active"`
	CreatedAt     int64  `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt     int64  `json:"updatedAt" gorm:"autoUpdateTime"`
}

// HomeAccessRule lets users (by email or client group) reach some targets of
// a home network; no targets means the whole network.
type HomeAccessRule struct {
	Emails  []string `json:"emails"`
	Groups  []string `json:"groups"`
	Targets []string `json:"targets"`
}
