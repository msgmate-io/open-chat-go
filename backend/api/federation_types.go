package api

import "time"

type RegisterNode struct {
	Name         string
	Addresses    []string
	AddToNetwork string
	LastChanged  *time.Time
}

type NodeInfo struct {
	Name      string
	Addresses []string
}

type IdentityResponse struct {
	ID                 string   `json:"id"`
	ConnectMultiadress []string `json:"connect_multiadress"`
}

type RegisterNodeRequest struct {
	Name         string   `json:"name"`
	Addresses    []string `json:"addresses"`
	AddToNetwork string   `json:"add_to_network"`
}

type PaginatedNodes struct {
	Data  []Node `json:"data"`
	Rows  []Node `json:"rows"`
	Total int64  `json:"total"`
}

type Node struct {
	ID            uint      `json:"id"`
	NodeName      string    `json:"node_name"`
	PeerID        string    `json:"peer_id"`
	LatestContact time.Time `json:"latest_contact,omitempty"`
}

type CreateAndStartProxyRequest struct {
	Kind          string `json:"kind"`
	TrafficOrigin string `json:"traffic_origin"`
	TrafficTarget string `json:"traffic_target"`
	NetworkName   string `json:"network_name"`
	Port          string `json:"port"`
	UseTLS        bool   `json:"use_tls"`
	SSHPassword   string `json:"ssh_password,omitempty"`
	Direction     string `json:"direction"`
	KeyPrefix     string `json:"key_prefix,omitempty"`
	ExpiresIn     int    `json:"expires_in,omitempty"`
	NodeUUID      string `json:"node_uuid,omitempty"`
}

type RequestNode struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type PaginatedProxies struct {
	Data  []Proxy `json:"data"`
	Total int64   `json:"total"`
}

type Proxy struct {
	ID            uint   `json:"id"`
	Kind          string `json:"kind"`
	TrafficOrigin string `json:"traffic_origin"`
	TrafficTarget string `json:"traffic_target"`
	NetworkName   string `json:"network_name"`
	Port          string `json:"port"`
	UseTLS        bool   `json:"use_tls"`
}

type RequestSelfUpdate struct {
	Message           string `json:"message"`
	BinaryOwnerPeerId string `json:"binary_owner_peer_id,omitempty"`
	NetworkName       string `json:"network_name,omitempty"`
}
