package entity

// PeerProbeRequest asks a node whether it reaches another node at any of the ports.
type PeerProbeRequest struct {
	Address string `json:"address" example:"de.example.com"`
	Ports   []int  `json:"ports"`
}

// PeerProbeResult is what the asked node saw; Port is the first one that answered.
type PeerProbeResult struct {
	Reachable bool   `json:"reachable" example:"true"`
	Port      int    `json:"port" example:"443"`
	LatencyMs int    `json:"latencyMs" example:"38"`
	Error     string `json:"error" example:""`
}
