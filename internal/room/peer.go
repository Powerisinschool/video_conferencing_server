package room

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
	"video_conferencing_server/internal/logger"
	"video_conferencing_server/internal/models"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// SignalPeer sends a WebSocket message to the specified peer
func SignalPeer(p *models.Peer, event models.WebsocketMessageEvent, data interface{}, encodeData bool) error {
	p.SocketLock.Lock()
	defer p.SocketLock.Unlock()

	var payload []byte
	if encodeData {
		payload, _ = json.Marshal(data)
	} else {
		payload = data.([]byte)
	}
	msg := models.WebSocketMessage{Event: event, Data: payload}
	return p.WebSocket.WriteJSON(msg)
}

// SignalPeer is a method on Room that sends a WebSocket message to the specified peer
func (r *Room) SignalPeer(p *models.Peer, event models.WebsocketMessageEvent, data interface{}, encodeData bool) error {
	return SignalPeer(p, event, data, encodeData)
}

var (
	ErrRoomIsNil  = errors.New("room is nil")
	ErrPeerExists = errors.New("peer already exists in room")
	ErrRoomFull   = errors.New("room is full")
)

// NewPeer creates a new Peer instance and adds it to the room
func (r *Room) InitializePeer(id string, displayName *string, ws *websocket.Conn, currentPeer *models.Peer) error {
	if r == nil {
		return ErrRoomIsNil
	}
	if currentPeer == nil {
		currentPeer = &models.Peer{
			ID:             uuid.New(),
			DisplayName:    displayName,
			PeerConnection: nil,
			Tracks:         []*webrtc.TrackLocalStaticRTP{},
			WebSocket:      ws,
			SocketLock:     sync.Mutex{},
			Done:           make(chan bool),
		}
	}
	if currentPeer != nil && !currentPeer.IsCreated() {
		currentPeer.ID = uuid.New()
		currentPeer.DisplayName = displayName
		currentPeer.PeerConnection = nil
		currentPeer.Tracks = []*webrtc.TrackLocalStaticRTP{}
		currentPeer.WebSocket = ws
		currentPeer.SocketLock = sync.Mutex{}
		currentPeer.SignalLock = sync.Mutex{}
		currentPeer.Done = make(chan bool)
	}
	// p := &models.Peer{
	// 	ID:             uuid.New(),
	// 	DisplayName:    displayName,
	// 	PeerConnection: nil,
	// 	Tracks:         []*webrtc.TrackLocalStaticRTP{},
	// 	WebSocket:      ws,
	// 	SocketLock:     sync.Mutex{},
	// 	Done:           make(chan bool),
	// }
	if _, exists := r.Peers[currentPeer.ID]; exists {
		return ErrPeerExists
	}
	if len(r.Peers) >= 10 {
		logger.LogError(ErrRoomFull.Error(), "roomId", r.ID)
		r.SignalPeer(currentPeer, "room-full", json.RawMessage(`"`+ErrRoomFull.Error()+`"`), false)
		return ErrRoomFull
	}
	err := r.newPeerConnection(currentPeer)
	if err != nil {
		return err
	}
	r.ListLock.Lock()
	defer r.ListLock.Unlock()
	r.Peers[currentPeer.ID] = currentPeer
	return nil
}

func (r *Room) newPeerConnection(p *models.Peer) error {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}
	streamID := fmt.Sprintf("stream-%s", p.ID.String())
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video",
		streamID,
	)
	if err != nil {
		logger.LogError("Error creating local video track", "error", err)
		return err
	}
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio",
		streamID,
	)
	if err != nil {
		logger.LogError("Error creating local audio track", "error", err)
		return err
	}
	p.Tracks = append(p.Tracks, videoTrack, audioTrack) // Will add tracks when receiving an offer
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		logger.LogInfo("Peer Connection State changed", "state", state.String(), "peerId", p.ID.String(), "roomId", r.ID)
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			r.RemovePeer(p)
			p.WebSocket.Close()
			logger.LogInfo("Peer disconnected", "peerId", p.ID.String(), "roomId", r.ID)
		}
	})
	peerConnection.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// This is the handler func for incoming tracks from the peer (audio and video)
		logger.LogInfo("Received remote track", "kind", remoteTrack.Kind().String(), "peerId", p.ID.String(), "roomId", r.ID)

		// r.ListLock.RLock()
		for _, otherPeer := range r.Peers {
			if otherPeer.ID == p.ID {
				continue // Don't send to self
			}
			logger.LogInfo("Forwarding track to peer", "toPeerId", otherPeer.ID.String(), "fromPeerId", p.ID.String())
			for _, track := range p.Tracks {
				if track.Kind() != remoteTrack.Kind() {
					continue
				}
				// if track.ID() == remoteTrack.ID() {
				// 	logger.Logger.Warn("Track already exists for peer, skipping", "peerId", otherPeer.ID.String(), "trackId", track.ID())
				// 	continue
				// }
				_, err = otherPeer.PeerConnection.AddTrack(track)
				if err != nil {
					logger.LogError("Error adding track to PeerConnection", "error", err, "toPeerId", otherPeer.ID.String(), "fromPeerId", p.ID.String())
				}
			}

			// Renegotiate with existing peer
			offer, err := otherPeer.PeerConnection.CreateOffer(nil)
			if err != nil {
				logger.LogError("Error creating renegotiation offer", "error", err, "peerId", otherPeer.ID.String())
				continue
			}

			err = otherPeer.PeerConnection.SetLocalDescription(offer)
			if err != nil {
				logger.LogError("Error setting local renegotiation description", "error", err, "peerId", otherPeer.ID.String())
				continue
			}

			err = SignalPeer(otherPeer, models.MessageTypeOffer, offer.SDP, true)
			if err != nil {
				logger.LogError("Error signaling peer with renegotiation offer", "error", err, "peerId", otherPeer.ID.String())
				continue
			}
			if remoteTrack.Kind() == webrtc.RTPCodecTypeVideo {
				go func() {
					if err := peerConnection.WriteRTCP([]rtcp.Packet{
						&rtcp.PictureLossIndication{MediaSSRC: uint32(remoteTrack.SSRC())},
					}); err != nil {
						logger.LogError("Error sending immediate PLI", "error", err)
					}
				}()
			}
		}
		// r.ListLock.RUnlock()

		// PLI Ticker
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					// Send a PLI on the "receiver"
					rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{
						&rtcp.PictureLossIndication{MediaSSRC: uint32(remoteTrack.SSRC())},
					})
					if rtcpSendErr != nil {
						logger.LogError("Error sending RTCP PLI", "error", rtcpSendErr)
					}
				case <-p.Done:
					return
				}
			}
		}()

		// RTP Pump
		go func() {
			raddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:4002") // Debugging UDP address for VLC (tap)
			debugConn, err := net.DialUDP("udp", nil, raddr)
			if err != nil {
				fmt.Println("Debug UDP Error:", err)
			}
			defer debugConn.Close()

			buf := make([]byte, 1500)
			for {
				select {
				case <-p.Done:
					return
				default:
					n, _, readErr := remoteTrack.Read(buf)
					if readErr != nil {
						logger.LogError("Error reading from remote track", "error", readErr)
						return
					}

					if debugConn != nil {
						debugConn.Write(buf[:n])
					}

					if remoteTrack.Kind() == webrtc.RTPCodecTypeVideo {
						if _, err := p.Tracks[0].Write(buf[:n]); err != nil {
							logger.LogError("Error writing to local video track", "error", err)
						}
					} else if remoteTrack.Kind() == webrtc.RTPCodecTypeAudio {
						if _, err := p.Tracks[1].Write(buf[:n]); err != nil {
							logger.LogError("Error writing to local audio track", "error", err)
						}
					}
				}
			}
		}()
	})
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		candidatePayload := models.Candidate{
			Candidate:     candidate.ToJSON().Candidate,
			SDPMid:        *candidate.ToJSON().SDPMid,
			SDPMLineIndex: *candidate.ToJSON().SDPMLineIndex,
		}
		candidateData, err := json.Marshal(candidatePayload)
		if err != nil {
			logger.LogError("Error marshaling ICE candidate", "error", err)
			return
		}
		err = SignalPeer(p, models.MessageTypeIceCandidate, candidateData, false)
		if err != nil {
			logger.LogError("Error sending ICE candidate", "error", err)
		}
	})
	p.PeerConnection = peerConnection
	return nil
}

var (
	ErrOfferFailedUnexpectedly = errors.New("failed to handle offer due to unexpected error")
	ErrOfferBeforeJoin         = errors.New("offer received before joining a room")
	ErrPeerConnectionNil       = errors.New("peer connection is nil")
	ErrAnswerBeforeJoin        = errors.New("answer received before joining a room")
	ErrIceCandidateBeforeJoin  = errors.New("ICE candidate received before joining a room")
)

// HandleOffer processes an incoming offer from a peer
func (r *Room) HandleOffer(p *models.Peer, offerData json.RawMessage) error {
	var data string
	if r == nil {
		logger.LogError("(`peer.go`) This error should never happen.. something is wrong") // Just to be safe
		return ErrOfferFailedUnexpectedly
	}
	if p == nil {
		logger.LogError("Peer is nil when handling offer.. should also never happen", "roomId", r.ID) // Just to be safe
		return ErrOfferFailedUnexpectedly
	}
	if !r.IsCreated() {
		logger.LogError("Room is not created when handling offer", "peerId", p.ID.String(), "roomId", r.ID) // This can happen
		return ErrOfferBeforeJoin
	}
	if !p.IsCreated() {
		logger.LogError("Peer is not created when handling offer", "roomId", r.ID, "peerId", p.ID.String()) // This can also happen
		return ErrOfferBeforeJoin
	}
	err := json.Unmarshal(offerData, &data)
	if err != nil {
		logger.LogError("Error unmarshaling offer data", "error", err)
		return err
	}
	if p.PeerConnection == nil {
		logger.LogError("PeerConnection is nil for peer", "peerId", p.ID.String())
		return ErrPeerConnectionNil
	}
	for _, otherPeer := range r.Peers {
		if otherPeer.ID == p.ID {
			continue
		}
		if len(otherPeer.Tracks) == 0 {
			continue
		}
		logger.LogInfo("Adding existing tracks from peer to new peer", "fromPeerId", otherPeer.ID.String(), "toPeerId", p.ID.String())
		for _, track := range otherPeer.Tracks {
			_, err = p.PeerConnection.AddTrack(track)
			if err != nil {
				logger.LogError("Error adding existing track to PeerConnection", "error", err, "toPeerId", p.ID.String(), "fromPeerId", otherPeer.ID.String())
			}
		}
	}
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  data,
	}
	err = p.PeerConnection.SetRemoteDescription(offer)
	if err != nil {
		logger.LogError("Error setting remote description", "error", err)
		return err
	}
	answer, err := p.PeerConnection.CreateAnswer(nil)
	if err != nil {
		logger.LogError("Error creating answer", "error", err)
		return err
	}
	err = p.PeerConnection.SetLocalDescription(answer)
	if err != nil {
		logger.LogError("Error setting local description", "error", err)
		return err
	}
	err = SignalPeer(p, models.MessageTypeAnswer, answer.SDP, true)
	if err != nil {
		logger.LogError("Error signaling peer with answer", "error", err)
		return err
	}
	return nil
}

// HandleAnswer processes an incoming answer from a peer
func (r *Room) HandleAnswer(p *models.Peer, answerData json.RawMessage) error {
	var data string
	if err := json.Unmarshal(answerData, &data); err != nil {
		logger.LogError("Error unmarshaling answer data", "error", err)
		return err
	}

	if p.PeerConnection.SignalingState() != webrtc.SignalingStateStable {
		answer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer,
			SDP:  data,
		}
		if err := p.PeerConnection.SetRemoteDescription(answer); err != nil {
			logger.LogError("Error setting remote description from answer", "error", err)
			return err
		}
	} else {
		logger.Logger.Warn("Received answer but signaling state is Stable (unexpected)", "peerId", p.ID.String())
	}
	return nil
}

// HandleIceCandidate processes an incoming ICE candidate from a peer
func (r *Room) HandleIceCandidate(p *models.Peer, candidateData json.RawMessage) error {
	var candidatePayload models.Candidate
	err := json.Unmarshal(candidateData, &candidatePayload)
	if err != nil {
		logger.LogError("Error unmarshaling ICE candidate data", "error", err)
		return err
	}
	if p.PeerConnection != nil {
		candidate := webrtc.ICECandidateInit{
			Candidate:     candidatePayload.Candidate,
			SDPMid:        &candidatePayload.SDPMid,
			SDPMLineIndex: &candidatePayload.SDPMLineIndex,
		}
		if err := p.PeerConnection.AddICECandidate(candidate); err != nil {
			logger.LogError("Error adding ICE candidate to PeerConnection", "error", err)
		}
	}
	return nil
}

func (r *Room) IsCreated() bool {
	return r.ID != ""
}

// RemovePeer removes a peer from the room and cleans up resources
func (r *Room) RemovePeer(p *models.Peer) {
	r.ListLock.Lock()
	defer r.ListLock.Unlock()

	peer, exists := r.Peers[p.ID]
	if !exists {
		logger.LogError("Attempted to remove non-existent peer", "peerId", p.ID.String())
		return
	}
	close(peer.Done)
	if peer.PeerConnection != nil {
		peer.PeerConnection.Close()
	}
	delete(r.Peers, p.ID)
	for _, p := range r.Peers {
		SignalPeer(p, "peer-left", p.ID.String(), true)
	}
	logger.LogInfo("Peer removed from room", "peerId", p.ID.String(), "roomId", r.ID)
}

// Broadcast sends a message to all peers in the room, optionally excluding one peer
func (r *Room) Broadcast(event models.WebsocketMessageEvent, data interface{}, excludePeerID *string) {
	r.ListLock.RLock()
	defer r.ListLock.RUnlock()

	for _, peer := range r.Peers {
		if excludePeerID != nil && peer.ID.String() == *excludePeerID {
			continue
		}
		SignalPeer(peer, event, data, true)
	}
}
