package model

// NodeJoinToken is a one-time credential the master hands out in an install
// command; a fresh server presents it once to register itself as a node.
type NodeJoinToken struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	TokenHash string `json:"-" gorm:"column:token_hash;uniqueIndex"`
	ExpiresAt int64  `json:"expiresAt" gorm:"column:expires_at"`
	UsedAt    int64  `json:"usedAt" gorm:"column:used_at"`
	NodeId    int    `json:"nodeId" gorm:"column:node_id"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime"`
}
