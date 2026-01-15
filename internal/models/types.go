package models

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type WebsocketMessageEvent string

const (
	MessageTypeOffer        WebsocketMessageEvent = "offer"
	MessageTypeAnswer       WebsocketMessageEvent = "answer"
	MessageTypeJoin         WebsocketMessageEvent = "join"
	MessageTypeIceCandidate WebsocketMessageEvent = "iceCandidate"
	MessageTypeLeave        WebsocketMessageEvent = "leave"
)

type WebSocketMessage struct {
	Event WebsocketMessageEvent `json:"event"` // "offer", "answer", "join", "iceCandidate"
	Data  json.RawMessage       `json:"data"`  // The payload (unmarshaled later based on Event)
}

type Candidate struct {
	Candidate     string `json:"candidate"`
	SDPMid        string `json:"sdpMid"`
	SDPMLineIndex uint16 `json:"sdpMLineIndex"`
}

type AccessDetails struct {
	Private   bool // This will affect whether we check the whitelist
	Locked    bool
	Password  *string
	Whitelist []uuid.UUID
}

type ManagementDetails struct {
	Owner     uuid.UUID
	Admin     []uuid.UUID
	CreatedAt time.Time
}

type Peer struct {
	ID             uuid.UUID
	DisplayName    *string
	PeerConnection *webrtc.PeerConnection
	Tracks         []*webrtc.TrackLocalStaticRTP
	WebSocket      *websocket.Conn
	SocketLock     sync.Mutex
	SignalLock     sync.Mutex
	// RoomID         string
	Done chan bool
}

func (p *Peer) IsCreated() bool {
	return p.ID != uuid.Nil
}

func (p *Peer) IsConnected() bool {
	return p.PeerConnection != nil && p.WebSocket != nil
}
