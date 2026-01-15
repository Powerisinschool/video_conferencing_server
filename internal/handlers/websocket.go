package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"video_conferencing_server/internal/logger"
	"video_conferencing_server/internal/models"
	"video_conferencing_server/internal/room"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type WebSocketHandler struct {
	Manager   *room.Manager
	Upgrader  websocket.Upgrader
	RTCConfig webrtc.Configuration
}

// NewWebSocketHandler creates a new WebSocket handler for managing peer connections
func NewWebSocketHandler(m *room.Manager, config webrtc.Configuration) *WebSocketHandler {
	return &WebSocketHandler{
		Manager:   m,
		RTCConfig: config,
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (h *WebSocketHandler) Handle(w http.ResponseWriter, r *http.Request) {
	conn, err := h.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.LogError("WebSocket upgrade error", "error", err)
		return
	}
	defer conn.Close()

	var currentPeer models.Peer               // We'll initialize this on join, I choose to keep it here for scope reasons (stack vs heap)
	var currentRoom *room.Room = &room.Room{} // Each connection is tied to a single room (we'll replace this on join)

	for {
		var message models.WebSocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			logger.LogError("WebSocket read error", "error", err)
			break
		}

		switch message.Event {
		case models.MessageTypeOffer:
			err := currentRoom.HandleOffer(&currentPeer, message.Data)
			if err != nil {
				logger.LogError("Offer handling error", "error", err)
			}
		case models.MessageTypeAnswer:
			if !currentPeer.IsCreated() || !currentRoom.IsCreated() {
				logger.LogError("Answer received before join", "peer", &currentPeer, "room", currentRoom)
				return
			}
			err := currentRoom.HandleAnswer(&currentPeer, message.Data)
			if err != nil {
				logger.LogError("Answer handling error", "error", err)
			}
		case models.MessageTypeIceCandidate:
			if !currentPeer.IsCreated() || !currentRoom.IsCreated() {
				logger.LogError("ICE candidate received before join", "peer", &currentPeer, "room", currentRoom)
				return
			}
			err := currentRoom.HandleIceCandidate(&currentPeer, message.Data)
			if err != nil {
				logger.LogError("ICE candidate handling error", "error", err)
			}
		case models.MessageTypeJoin:
			currentRoom, err = h.handleJoin(conn, message.Data, &currentPeer, currentRoom) // Updates currentRoom and currentPeer
			if err != nil {
				logger.LogError("Join handling error", "error", err)
				return
			}
			// currentPeer = peer
		case models.MessageTypeLeave:
			if currentPeer.IsCreated() && currentRoom.IsCreated() {
				currentRoom.RemovePeer(&currentPeer)
				logger.LogInfo("Peer left room", "peerId", currentPeer.ID.String(), "roomId", currentRoom.ID)
			}
			return
		default:
			logger.LogError("Unknown message event", "event", message.Event)
		}
	}
	if currentPeer.IsCreated() && currentRoom.IsCreated() {
		currentRoom.RemovePeer(&currentPeer)
		logger.LogInfo("Peer disconnected and removed from room", "peerId", currentPeer.ID.String(), "roomId", currentRoom.ID)
		if len(currentRoom.Peers) == 0 {
			h.Manager.DeleteRoom(currentRoom.ID)
			logger.LogInfo("Room deleted as it became empty", "roomId", currentRoom.ID)
		}
	}
}

func (h *WebSocketHandler) handleJoin(conn *websocket.Conn, data json.RawMessage, currentPeer *models.Peer, currentRoom *room.Room) (*room.Room, error) {
	var payload struct {
		RoomID string `json:"roomId"`
		PeerID string `json:"peerId"`
	}
	err := json.Unmarshal(data, &payload)
	if err != nil {
		logger.LogError("Error unmarshaling join data", "error", err)
		return currentRoom, err
	}
	// Create or get the room
	if r := h.Manager.GetRoom(payload.RoomID); r != nil {
		currentRoom = r
	} else {
		h.Manager.CreateRoom(payload.RoomID, room.DefaultRoomCapacity, currentRoom)
		if !currentRoom.IsCreated() {
			logger.LogError("Room creation failed", "roomId", payload.RoomID)
			return currentRoom, errors.New("room creation failed")
		}
	}
	err = currentRoom.InitializePeer(payload.PeerID, nil, conn, currentPeer)
	if err != nil {
		if errors.Is(err, room.ErrPeerExists) {
			logger.LogError("Peer already exists in room", "peerId", payload.PeerID, "roomId", payload.RoomID)
		} else if errors.Is(err, room.ErrRoomFull) {
			logger.LogError("Room is full", "roomId", payload.RoomID)
		} else {
			logger.LogError("Error creating new peer", "error", err)
		}
		if currentPeer.IsCreated() {
			currentRoom.SignalPeer(currentPeer, "peer-id", currentPeer.ID.String(), true)
		}
		return currentRoom, err
	}
	currentRoom.SignalPeer(currentPeer, "peer-id", currentPeer.ID.String(), true)
	logger.LogInfo("Peer joined room", "peerId", currentPeer.ID.String(), "roomId", currentRoom.ID)
	logger.LogInfo("Current number of peers in room", "count", len(currentRoom.Peers))
	fmt.Println("peer:", currentPeer)
	return currentRoom, nil
}
