package model

// Usage kinds: a client by email, or a server by node id ("0" = this panel).
const (
	TrafficUsageClient = "client"
	TrafficUsageNode   = "node"
)

// TrafficUsageHour is the traffic one client or server moved in one hour
// starting at Bucket (unix seconds); it survives counter resets.
type TrafficUsageHour struct {
	Id      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Kind    string `json:"kind" gorm:"size:16;not null;uniqueIndex:idx_usage_hour_key,priority:1"`
	Subject string `json:"subject" gorm:"not null;uniqueIndex:idx_usage_hour_key,priority:2"`
	Bucket  int64  `json:"bucket" gorm:"not null;uniqueIndex:idx_usage_hour_key,priority:3;index:idx_usage_hour_bucket"`
	Up      int64  `json:"up"`
	Down    int64  `json:"down"`
}

func (TrafficUsageHour) TableName() string { return "traffic_usage_hours" }

// TrafficUsageDay is hourly usage folded into one panel-timezone day starting at Bucket.
type TrafficUsageDay struct {
	Id      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Kind    string `json:"kind" gorm:"size:16;not null;uniqueIndex:idx_usage_day_key,priority:1"`
	Subject string `json:"subject" gorm:"not null;uniqueIndex:idx_usage_day_key,priority:2"`
	Bucket  int64  `json:"bucket" gorm:"not null;uniqueIndex:idx_usage_day_key,priority:3;index:idx_usage_day_bucket"`
	Up      int64  `json:"up"`
	Down    int64  `json:"down"`
}

func (TrafficUsageDay) TableName() string { return "traffic_usage_days" }
