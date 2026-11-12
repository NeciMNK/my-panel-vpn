package service

import (
	"encoding/json"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// ErrInboundNotGrpc rejects a gRPC→XHTTP copy of an inbound on another transport.
var ErrInboundNotGrpc = common.NewError("inbound does not use the gRPC transport")

// grpcStreamToXhttp swaps the gRPC transport for XHTTP and keeps security, sockopt
// and finalmask: serviceName becomes the path, authority the Host header.
func grpcStreamToXhttp(streamSettings string) (string, error) {
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return "", err
	}
	if network, _ := stream["network"].(string); network != "grpc" {
		return "", ErrInboundNotGrpc
	}
	grpc, _ := stream["grpcSettings"].(map[string]any)
	serviceName, _ := grpc["serviceName"].(string)
	authority, _ := grpc["authority"].(string)
	delete(stream, "grpcSettings")
	stream["network"] = "xhttp"
	stream["xhttpSettings"] = map[string]any{
		"path":    "/" + strings.TrimPrefix(serviceName, "/"),
		"host":    authority,
		"mode":    "auto",
		"headers": map[string]any{},
	}
	out, err := json.MarshalIndent(stream, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// CopyGrpcInboundAsXhttp adds an XHTTP copy of a gRPC inbound on port, on the same
// node and with the same clients; the source keeps serving until it is deleted.
func (s *InboundService) CopyGrpcInboundAsXhttp(id, port int) (*model.Inbound, bool, error) {
	src, err := s.GetInbound(id)
	if err != nil {
		return nil, false, err
	}
	stream, err := grpcStreamToXhttp(src.StreamSettings)
	if err != nil {
		return nil, false, err
	}
	clients, err := s.GetClients(src)
	if err != nil {
		return nil, false, err
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(src.Settings), &settings); err != nil {
		return nil, false, err
	}
	settings["clients"] = []any{}
	emptySettings, err := json.Marshal(settings)
	if err != nil {
		return nil, false, err
	}

	copied := &model.Inbound{
		UserId:            src.UserId,
		Remark:            src.Remark + " (XHTTP)",
		SubSortIndex:      src.SubSortIndex,
		ExcludeFromSub:    src.ExcludeFromSub,
		Enable:            true,
		Total:             src.Total,
		ExpiryTime:        src.ExpiryTime,
		TrafficReset:      src.TrafficReset,
		TrafficResetDay:   src.TrafficResetDay,
		Listen:            src.Listen,
		Port:              port,
		Protocol:          src.Protocol,
		Settings:          string(emptySettings),
		StreamSettings:    stream,
		Sniffing:          src.Sniffing,
		NodeID:            src.NodeID,
		ShareAddrStrategy: src.ShareAddrStrategy,
		ShareAddr:         src.ShareAddr,
		DisableFlow:       src.DisableFlow,
	}
	copied, needRestart, err := s.AddInbound(copied)
	if err != nil {
		return nil, false, err
	}
	if len(clients) == 0 {
		return copied, needRestart, nil
	}
	emails := make([]string, 0, len(clients))
	for _, c := range clients {
		emails = append(emails, c.Email)
	}
	res, attachRestart, err := s.clientService.BulkAttach(s, emails, []int{copied.Id})
	if err != nil {
		return copied, needRestart, err
	}
	if len(res.Errors) > 0 {
		return copied, needRestart || attachRestart, common.NewError("XHTTP inbound created, but some clients were not attached:", strings.Join(res.Errors, "; "))
	}
	return copied, needRestart || attachRestart, nil
}
