package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
	Peers  map[string]*webrtc.PeerConnection
	Tracks map[string]*webrtc.TrackLocalStaticRTP
}

var upgrader websocket.Upgrader
var config webrtc.Configuration
var rooms map[string]*Room

func handleWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error upgrading:", err)
		return
	}
	defer conn.Close()

	var peerConnection *webrtc.PeerConnection
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
			fmt.Println("Received offer:", data)
			peerConnection, err = webrtc.NewPeerConnection(config)
			if err != nil {
				fmt.Println("Error creating PeerConnection:", err)
				continue
			}
			peerID := "peerID" // Replace with actual peer ID from your logic
			room.Peers[peerID] = peerConnection
			room.Tracks[peerID], err = webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video", "pion")
			if err != nil {
				fmt.Println("Error creating local track:", err)
				continue
			}
			_, err = peerConnection.AddTrack(room.Tracks[peerID])
			if err != nil {
				fmt.Println("Error adding local track to PeerConnection:", err)
				continue
			}
			done := make(chan bool) // Channel to signal goroutines to stop (on peer disconnect)
			peerConnection.OnTrack(func(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
				fmt.Println("Received track:", tr.Kind())
				fmt.Println("Received track:", tr.Kind().String())
				// PLI Ticker
				go func() {
					ticker := time.NewTicker(3 * time.Second)
					defer ticker.Stop()

					for {
						select {
						case <-ticker.C:
							// Send a PLI on the "receiver"
							fmt.Printf("Track ID: %s, Kind: %s\n", tr.ID(), tr.Kind().String())
							rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{
								&rtcp.PictureLossIndication{MediaSSRC: uint32(tr.SSRC())},
							})
							if rtcpSendErr != nil {
								fmt.Println("Error sending RTCP:", rtcpSendErr)
							}
						case <-done:
							return
						}
					}
				}()

				// RTP Pump
				go func() {
					buf := make([]byte, 1500)
					for {
						n, _, readErr := tr.Read(buf)
						if readErr != nil {
							fmt.Println("Error reading from track:", readErr)
							close(done) // Signal other goroutine to stop
							return
						}
						for otherPeerID, localTrack := range room.Tracks {
							if otherPeerID == peerID {
								continue // Don't send to self
							}
							if _, err := localTrack.Write(buf[:n]); err != nil {
								fmt.Println("Error writing to local track:", err)
							}
						}
					}
				}()
			}) // Handle incoming video tracks
			peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
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
			peerConnection.SetRemoteDescription(offer)
			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				fmt.Println("Error creating answer:", err)
				continue
			}
			err = peerConnection.SetLocalDescription(answer)
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
		case "answer":
			// Client is responding to us
			fmt.Println("Client is responding to us")
		case "iceCandidate":
			if peerConnection != nil {
				// peerConnection.AddICECandidate()
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

			if _, exists := rooms[payload.RoomID]; !exists {
				rooms[payload.RoomID] = &Room{
					Peers:  make(map[string]*webrtc.PeerConnection),
					Tracks: make(map[string]*webrtc.TrackLocalStaticRTP),
				}
			}
			room = rooms[payload.RoomID]
		default:
			fmt.Println("Unsupported event specified:", message.Event)
		}
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
	fmt.Println("WebSocket server started on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error starting server:", err)
	}
}
