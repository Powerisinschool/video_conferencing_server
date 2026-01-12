package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

type WebSocketMessage struct {
	Event string          `json:"event"` // "offer", "answer", "join", "iceCandidate"
	Data  json.RawMessage `json:"data"`  // The payload (unmarshaled later based on Event)
}

type Candidate struct {
	Candidate     string `json:"candidate"`
	SDPMid        string `json:"sdpMid"`
	SDPMLineIndex uint16 `json:"sdpMLineIndex"`
}

type Room struct {
	ListLock sync.RWMutex
	Peers    map[uuid.UUID]*Peer
}

type Peer struct {
	ID             uuid.UUID
	PeerConnection *webrtc.PeerConnection
	Track          *webrtc.TrackLocalStaticRTP
	WebSocket      *websocket.Conn
	SocketLock     sync.Mutex
	// RoomID         string
	Done chan bool
}

var upgrader websocket.Upgrader
var config webrtc.Configuration
var rooms map[string]*Room
var roomsLock sync.RWMutex

func createPeerID() uuid.UUID {
	// return fmt.Sprintf("peer-%d", time.Now().UnixNano())
	return uuid.New()
}

func cleanupPeer(peerID uuid.UUID, room *Room) {
	fmt.Println("Cleaning up peer:", peerID.String())
	room.ListLock.Lock()
	defer room.ListLock.Unlock()

	peer, exists := room.Peers[peerID]

	if !exists {
		return
	}

	fmt.Println("Removing peer connection for peer:", peer.ID.String())
	close(peer.Done)
	peer.PeerConnection.Close()
	delete(room.Peers, peerID)

	peerLeftMsg := WebSocketMessage{
		Event: "remove-peer",
		Data:  json.RawMessage(fmt.Sprintf(`"%s"`, peerID.String())),
	}

	for _, p := range room.Peers {
		// We use a helper or lock here to ensure thread safety (see Part 2)
		p.SocketLock.Lock()
		if err := p.WebSocket.WriteJSON(peerLeftMsg); err != nil {
			fmt.Println("Error sending remove-peer message:", err)
		}
		p.SocketLock.Unlock()
	}
	fmt.Println("Cleaned up peer:", peerID.String())
}

func handleWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error upgrading:", err)
		return
	}
	defer conn.Close()

	var peer *Peer
	var room *Room
	for {
		var message WebSocketMessage
		err := conn.ReadJSON(&message)
		if err != nil {
			fmt.Println("Error reading websocket message:", err)
			break
		}

		switch message.Event {
		case "offer":
			// Client wants to start a call
			var data string
			err := json.Unmarshal(message.Data, &data)
			if err != nil {
				fmt.Println("Error unmarshaling offer data:", err)
				continue
			}
			// fmt.Println("Received offer:", data)
			if room == nil {
				fmt.Println("No room joined yet for this peer")
				continue
			}
			if len(room.Peers) >= 4 {
				// Room is full
				fullMsg := WebSocketMessage{
					Event: "room-full",
					Data:  json.RawMessage(`"Room is full"`),
				}
				conn.WriteJSON(fullMsg)
				continue
			}
			room.ListLock.Lock()
			peerConnection, err := webrtc.NewPeerConnection(config)
			if err != nil {
				fmt.Println("Error creating PeerConnection:", err)
				continue
			}
			peerID := createPeerID()
			peerIDMsg, _ := json.Marshal(peerID.String())
			conn.WriteJSON(WebSocketMessage{
				Event: "peer-id",
				Data:  peerIDMsg,
			})
			streamID := fmt.Sprintf("stream-%s", peerID.String())
			localTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video", streamID)
			peer = &Peer{
				ID:             peerID,
				PeerConnection: peerConnection,
				Track:          localTrack,
				WebSocket:      conn,
				Done:           make(chan bool),
			}
			room.Peers[peer.ID] = peer
			room.ListLock.Unlock()

			for _, otherPeer := range room.Peers {
				if otherPeer.ID == peer.ID {
					continue
				}
				if otherPeer.Track == nil {
					continue
				}
				fmt.Println("Found existing track in room:", otherPeer.Track.ID(), "belonging to peer:", otherPeer.ID.String())
				_, err = peer.PeerConnection.AddTrack(otherPeer.Track)
				if err != nil {
					fmt.Println("Error adding existing track to PeerConnection:", err)
				}
			}

			peer.PeerConnection.OnTrack(func(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) { // Handle incoming video tracks
				fmt.Println("Received track:", tr.Kind())
				// peer.Track, err = webrtc.NewTrackLocalStaticRTP(tr.Codec().RTPCodecCapability, tr.ID(), tr.StreamID())
				// if err != nil {
				// 	fmt.Println("Error creating local track:", err)
				// 	return
				// }

				room.ListLock.Lock()
				for _, otherPeer := range room.Peers {
					if otherPeer == peer {
						continue // Don't send to self
					}

					fmt.Println("Adding track to other peer:", otherPeer.ID.String())
					_, err = otherPeer.PeerConnection.AddTrack(peer.Track)
					if err != nil {
						fmt.Println("Error adding track to PeerConnection:", err)
					}

					// Renegotiate with existing peer
					offer, err := otherPeer.PeerConnection.CreateOffer(nil)
					if err != nil {
						fmt.Println("Error creating renegotiation offer:", err)
						continue
					}

					err = otherPeer.PeerConnection.SetLocalDescription(offer)
					if err != nil {
						fmt.Println("Error setting local renegotiation description:", err)
						continue
					}

					offerData, _ := json.Marshal(offer.SDP)
					msg := WebSocketMessage{
						Event: "offer",
						Data:  offerData,
					}

					otherPeer.SocketLock.Lock()
					err = otherPeer.WebSocket.WriteJSON(msg)
					otherPeer.SocketLock.Unlock()

					if err != nil {
						fmt.Println("Error sending renegotiation offer:", err)
					}
				}
				room.ListLock.Unlock()

				// PLI Ticker
				go func() {
					ticker := time.NewTicker(3 * time.Second)
					defer ticker.Stop()

					for {
						select {
						case <-ticker.C:
							// Send a PLI on the "receiver"
							fmt.Printf("Track ID: %s, Kind: %s\n", tr.ID(), tr.Kind().String())
							rtcpSendErr := peer.PeerConnection.WriteRTCP([]rtcp.Packet{
								&rtcp.PictureLossIndication{MediaSSRC: uint32(tr.SSRC())},
							})
							if rtcpSendErr != nil {
								fmt.Println("Error sending RTCP:", rtcpSendErr)
							}
						case <-peer.Done:
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
						case <-peer.Done:
							return
						default:
							n, _, readErr := tr.Read(buf)
							if readErr != nil {
								fmt.Println("Error reading from track:", readErr)
								return
							}

							if debugConn != nil {
								debugConn.Write(buf[:n])
							}

							room.ListLock.Lock()
							// for _, targetPeer := range room.Peers {
							// 	if targetPeer.ID == peer.ID {
							// 		continue // Don't send to self
							// 	}
							// 	if _, err := targetPeer.Track.Write(buf[:n]); err != nil {
							// 		fmt.Println("Error writing to local track:", err)
							// 	}
							// }
							if _, err := peer.Track.Write(buf[:n]); err != nil {
								fmt.Println("Error writing to local track:", err)
							}
							room.ListLock.Unlock()
						}
					}
				}()
			})

			peer.PeerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
				if c == nil {
					return
				}
				candidate := Candidate{
					Candidate:     c.ToJSON().Candidate,
					SDPMid:        *c.ToJSON().SDPMid,
					SDPMLineIndex: *c.ToJSON().SDPMLineIndex,
				}
				candidateData, err := json.Marshal(candidate)
				if err != nil {
					fmt.Println("Error marshaling ICE candidate:", err)
					return
				}
				resp := WebSocketMessage{
					Event: "iceCandidate",
					Data:  candidateData,
				}
				err = conn.WriteJSON(resp)
				if err != nil {
					fmt.Println("Error sending ICE candidate:", err)
				}
			})
			offer := webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  data,
			}
			peer.PeerConnection.SetRemoteDescription(offer)
			answer, err := peer.PeerConnection.CreateAnswer(nil)
			if err != nil {
				fmt.Println("Error creating answer:", err)
				continue
			}
			err = peer.PeerConnection.SetLocalDescription(answer)
			if err != nil {
				fmt.Println("Error setting local description:", err)
				continue
			}
			answerData, err := json.Marshal(answer.SDP)
			if err != nil {
				fmt.Println("Error marshaling answer data:", err)
				continue
			}
			resp := WebSocketMessage{
				Event: "answer",
				Data:  answerData,
			}
			err = conn.WriteJSON(resp)
			if err != nil {
				fmt.Println("Error sending answer:", err)
			}
			peer.PeerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
				fmt.Println("Peer Connection State has changed:", state.String())
				if state == webrtc.PeerConnectionStateClosed ||
					state == webrtc.PeerConnectionStateFailed ||
					state == webrtc.PeerConnectionStateDisconnected {
					cleanupPeer(peer.ID, room)
				}
			})
		case "answer":
			// Client is responding to our renegotiation offer
			var answerData string
			if err := json.Unmarshal(message.Data, &answerData); err != nil {
				fmt.Println("Error unmarshaling answer data:", err)
				continue
			}

			if peer.PeerConnection.SignalingState() != webrtc.SignalingStateStable {
				answer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  answerData,
				}
				if err := peer.PeerConnection.SetRemoteDescription(answer); err != nil {
					fmt.Println("Error setting remote description from answer:", err)
				}
			} else {
				fmt.Println("Received answer but signaling state is Stable (unexpected)")
			}
		case "iceCandidate":
			var candidatePayload Candidate
			err := json.Unmarshal(message.Data, &candidatePayload)
			if err != nil {
				fmt.Println(err)
				continue
			}
			if peer.PeerConnection != nil {
				candidate := webrtc.ICECandidateInit{
					Candidate:     candidatePayload.Candidate,
					SDPMid:        &candidatePayload.SDPMid,
					SDPMLineIndex: &candidatePayload.SDPMLineIndex,
				}
				if err := peer.PeerConnection.AddICECandidate(candidate); err != nil {
					fmt.Println(err)
				}
			}
		case "join":
			// Client wants to join a room
			var payload struct {
				RoomID string `json:"roomId"`
				PeerID string `json:"peerId"`
			}
			err := json.Unmarshal(message.Data, &payload)
			if err != nil {
				fmt.Println("Error unmarshaling join data:", err)
				continue
			}

			roomsLock.Lock()
			if _, exists := rooms[payload.RoomID]; !exists {
				fmt.Println("Creating new room:", payload.RoomID)
				rooms[payload.RoomID] = &Room{
					Peers: make(map[uuid.UUID]*Peer),
				}
			}
			room = rooms[payload.RoomID]
			if len(room.Peers) >= 4 {
				// Room is full
				fullMsg := WebSocketMessage{
					Event: "room-full",
					Data:  json.RawMessage(`"Room is full"`),
				}
				conn.WriteJSON(fullMsg)
				roomsLock.Unlock()
				continue
			}
			roomsLock.Unlock()
		case "leave":
			// Client wants to leave the room
			if peer != nil && room != nil {
				cleanupPeer(peer.ID, room)
			}
		default:
			fmt.Println("Unsupported event specified:", message.Event)
		}
	}
	if peer.PeerConnection != nil && room != nil {
		cleanupPeer(peer.ID, room)
	}
}

func main() {
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	config = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	rooms = make(map[string]*Room)

	http.HandleFunc("/ws", handleWebsocket)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)
	fmt.Println("WebSocket server started on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error starting server:", err)
	}
}
