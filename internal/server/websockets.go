package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/providers"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

const (
	// maxDisplayingAckTimeoutSeconds is a safety cap for v1+ firmware when displaying
	// ACK never arrives (dropped image, firmware bug). Long enough for very long WebP
	// animations but prevents indefinite rotation stall.
	maxDisplayingAckTimeoutSeconds = 600
)

type WSMessage struct {
	Queued     *int        `json:"queued"`
	Displaying *int        `json:"displaying"`
	Counter    *int        `json:"counter"`
	ClientInfo *ClientInfo `json:"client_info"`
}

type ClientInfo struct {
	FirmwareVersion    string  `json:"firmware_version"`
	FirmwareType       string  `json:"firmware_type"`
	ProtocolVersion    *int    `json:"protocol_version"`
	MACAddress         string  `json:"mac"`
	SSID               *string `json:"ssid"`
	WifiPowerSave      *int    `json:"wifi_power_save"`
	SkipDisplayVersion *bool   `json:"skip_display_version"`
	SkipBootAnimation  *bool   `json:"skip_boot_animation"`
	APMode             *bool   `json:"ap_mode"`
	PreferIPv6         *bool   `json:"prefer_ipv6"`
	SwapColors         *bool   `json:"swap_colors"`
	ColorOrder         *string `json:"color_order"`
	DisableTouch       *bool   `json:"disable_touch"`
	TouchBeep          *bool   `json:"touch_beep"`
	StartupSound       *bool   `json:"startup_sound"`
	ImageURL           *string `json:"image_url"`
	Hostname           *string `json:"hostname"`
	SNTPServer         *string `json:"sntp_server"`
	SyslogAddr         *string `json:"syslog_addr"`
}

type WSEvent struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id,omitempty"`
	AppID    string `json:"app_id,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

type DeviceCommandMessage struct {
	Payload []byte
}

type DevicePreviewMessage struct {
	App    *data.App
	Chunks []providers.RenderChunk
}

// handleWS upgrades HTTP connections to WebSockets for live image streaming, notifications, and device commands.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")

	device, err := s.reloadDevice(deviceID)
	if err != nil {
		slog.Warn("WS connection rejected", "id", deviceID, "error", err)
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	if device.RequireAPIKey {
		if key := extractDeviceKey(r); key == "" || key != device.APIKey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	user, err := gorm.G[data.User](s.DB).Where("username = ?", device.Username).First(r.Context())
	if err != nil {
		slog.Error("User for device not found in WS handler", "username", device.Username, "error", err)
		http.Error(w, "Internal Server Error: device owner not found", http.StatusInternalServerError)
		return
	}

	conn, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS upgrade failed", "error", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close WS connection", "error", err)
		}
	}()

	s.metrics.wsConnections.Inc()
	defer s.metrics.wsConnections.Dec()

	slog.Info("WS Connected", "device", deviceID)

	// Update protocol type if different from current
	if device.Info.ProtocolType != data.ProtocolWS {
		slog.Info("Updating protocol_type to WS on connect", "device", deviceID)
		device.Info.ProtocolType = data.ProtocolWS
		if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "info", device.Info); err != nil {
			slog.Error("Failed to update protocol_type", "error", err)
		}
	}
	ch := s.Broadcaster.Subscribe(deviceID)
	defer s.Broadcaster.Unsubscribe(deviceID, ch)

	ackCh := make(chan WSMessage, 10)
	stopCh := make(chan struct{})

	// Read loop to handle ping/pong/close and client messages
	go func() {
		defer close(stopCh)
		for {
			var msg WSMessage
			if err := conn.ReadJSON(&msg); err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Info("WS closed unexpectedly", "error", err)
				}
				return
			}

			// Update LastSeen
			if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "last_seen", time.Now()); err != nil {
				slog.Error("Failed to update last_seen", "error", err)
			}

			// Handle Message
			if msg.ClientInfo != nil {
				// Update Device Info
				device.Info.FirmwareVersion = msg.ClientInfo.FirmwareVersion
				device.Info.FirmwareType = msg.ClientInfo.FirmwareType
				if msg.ClientInfo.ProtocolVersion != nil {
					device.Info.ProtocolVersion = msg.ClientInfo.ProtocolVersion
				}
				device.Info.MACAddress = msg.ClientInfo.MACAddress

				if msg.ClientInfo.SSID != nil {
					device.Info.SSID = msg.ClientInfo.SSID
				}
				if msg.ClientInfo.WifiPowerSave != nil {
					device.Info.WifiPowerSave = msg.ClientInfo.WifiPowerSave
				}
				if msg.ClientInfo.SkipDisplayVersion != nil {
					device.Info.SkipDisplayVersion = msg.ClientInfo.SkipDisplayVersion
				}
				if msg.ClientInfo.SkipBootAnimation != nil {
					device.Info.SkipBootAnimation = msg.ClientInfo.SkipBootAnimation
				}
				if msg.ClientInfo.APMode != nil {
					device.Info.APMode = msg.ClientInfo.APMode
				}
				if msg.ClientInfo.PreferIPv6 != nil {
					device.Info.PreferIPv6 = msg.ClientInfo.PreferIPv6
				}
				if msg.ClientInfo.SwapColors != nil {
					device.Info.SwapColors = msg.ClientInfo.SwapColors
				}
				if msg.ClientInfo.ColorOrder != nil {
					// Store the canonical lower-case form so the settings page
					// shows what the device is actually using. A value this
					// server does not recognise is ignored rather than stored,
					// since the page could not represent it anyway.
					if order, ok := normalizeColorOrder(*msg.ClientInfo.ColorOrder); ok {
						device.Info.ColorOrder = &order
					}
				}
				if msg.ClientInfo.DisableTouch != nil {
					device.Info.DisableTouch = msg.ClientInfo.DisableTouch
				}
				if msg.ClientInfo.TouchBeep != nil {
					device.Info.TouchBeep = msg.ClientInfo.TouchBeep
				}
				if msg.ClientInfo.StartupSound != nil {
					device.Info.StartupSound = msg.ClientInfo.StartupSound
				}
				if msg.ClientInfo.ImageURL != nil {
					device.Info.ImageURL = msg.ClientInfo.ImageURL
				}
				if msg.ClientInfo.Hostname != nil {
					device.Info.Hostname = msg.ClientInfo.Hostname
				}
				if msg.ClientInfo.SNTPServer != nil {
					device.Info.SNTPServer = msg.ClientInfo.SNTPServer
				}
				if msg.ClientInfo.SyslogAddr != nil {
					device.Info.SyslogAddr = msg.ClientInfo.SyslogAddr
				}

				if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "info", device.Info); err != nil {
					slog.Error("Failed to update device info", "error", err)
				} else {
					s.Broadcaster.Notify(device.ID, nil)
				}
			}

			if msg.Queued != nil {
				// If we get a queued message, it's a new firmware device.
				// Update protocol version if not set.
				if device.Info.ProtocolVersion == nil {
					slog.Info("First 'queued' message, setting protocol_version to 1", "device", deviceID)
					newVersion := 1
					device.Info.ProtocolVersion = &newVersion
					if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "info", device.Info); err != nil {
						slog.Error("Failed to update device info (protocol version)", "error", err)
					} else {
						s.Broadcaster.Notify(device.ID, nil)
					}
				}
			}

			if msg.Displaying != nil || msg.Counter != nil {
				select {
				case ackCh <- msg:
				default:
				}
			}
		}
	}()

	s.wsWriteLoop(r.Context(), conn, device, &user, ackCh, ch, stopCh, s.GetBaseURL(r))
}

// baseURL is this server's address as the device connected to it, captured
// when the socket opened — the write loop outlives the request that made it.
func (s *Server) wsWriteLoop(ctx context.Context, conn *websocket.Conn, initialDevice *data.Device, user *data.User, ackCh <-chan WSMessage, broadcastCh <-chan any, stopCh <-chan struct{}, baseURL string) {
	var pendingImage []byte
	var pendingPreview *DevicePreviewMessage
	device := *initialDevice
	lastSentBrightness := -1
	lastSentVolume := -1
	// Send any pending OTA update on connection
	if device.PendingUpdateURL != "" {
		updateURL := device.PendingUpdateURL
		slog.Info("Sending pending OTA update on WS connect", "device", device.ID, "url", updateURL)
		payload := map[string]string{"ota_url": updateURL}
		if jsonPayload, err := json.Marshal(payload); err == nil {
			if err := conn.WriteMessage(websocket.TextMessage, jsonPayload); err == nil {
				if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "pending_update_url", ""); err != nil {
					slog.Error("Failed to clear pending update", "error", err)
				} else {
					device.PendingUpdateURL = ""
				}
			}
		}
	}
	sendImmediate := false

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		// Calculate effective brightness
		effectiveBrightness := device.GetEffectiveBrightness()

		// If a preview was pushed (single or multi-chunk), stream its chunks sequentially
		if pendingPreview != nil {
			previewMsg := pendingPreview
			pendingPreview = nil

			previewInterrupted := false
			for chunkIdx, chunk := range previewMsg.Chunks {
				select {
				case <-stopCh:
					return
				case <-ctx.Done():
					return
				default:
				}

				// Drain any stale unread ACKs before sending next chunk
				for {
					select {
					case <-ackCh:
					default:
						goto drainedPreview
					}
				}
			drainedPreview:

				chunkDwell := max(1, chunk.DurationMs/1000)
				if err := conn.WriteJSON(map[string]any{"dwell_secs": chunkDwell}); err != nil {
					slog.Error("Failed to write metadata WS message for preview chunk", "chunk", chunkIdx, "error", err)
					return
				}

				effectiveBrightness = device.GetEffectiveBrightness()
				if effectiveBrightness != lastSentBrightness {
					if err := conn.WriteJSON(map[string]int{"brightness": effectiveBrightness}); err != nil {
						slog.Error("Failed to write brightness WS message", "error", err)
						return
					}
					lastSentBrightness = effectiveBrightness
				}

				if err := conn.WriteMessage(websocket.BinaryMessage, chunk.WebPBytes); err != nil {
					slog.Error("Failed to write preview chunk binary message to WS", "chunk", chunkIdx, "error", err)
					return
				}

				if chunkIdx == 0 && sendImmediate {
					if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
						slog.Error("Failed to write immediate WS message for preview chunk", "chunk", chunkIdx, "error", err)
						return
					}
					sendImmediate = false
				} else if chunkIdx > 0 && device.Info.ProtocolVersion == nil {
					if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
						slog.Error("Failed to write immediate WS message for legacy preview chunk", "chunk", chunkIdx, "error", err)
						return
					}
				}

				chunkDuration := time.Duration(chunk.DurationMs) * time.Millisecond
				if chunkDuration <= 0 {
					chunkDuration = time.Duration(chunkDwell) * time.Second
				}

				var timer *time.Timer
				var timerC <-chan time.Time
				if device.Info.ProtocolVersion != nil {
					if chunkIdx == 0 {
						timer = time.NewTimer(time.Duration(maxDisplayingAckTimeoutSeconds) * time.Second)
					} else {
						prevDuration := time.Duration(previewMsg.Chunks[chunkIdx-1].DurationMs) * time.Millisecond
						if prevDuration <= 0 {
							prevDuration = time.Duration(max(1, previewMsg.Chunks[chunkIdx-1].DurationMs/1000)) * time.Second
						}
						timer = time.NewTimer(prevDuration + 10*time.Second)
					}
					timerC = timer.C
				} else {
					timer = time.NewTimer(chunkDuration)
					timerC = timer.C
				}

				waitingChunk := true
				for waitingChunk {
					select {
					case <-stopCh:
						if timer != nil {
							timer.Stop()
						}
						return
					case <-ctx.Done():
						if timer != nil {
							timer.Stop()
						}
						return
					case msg := <-ackCh:
						if msg.Displaying != nil || msg.Counter != nil {
							if timer != nil {
								timer.Stop()
							}
							if chunkIdx == 0 && previewMsg.App != nil {
								slog.Debug("Received ACK for preview chunk 0, updating DisplayingApp", "app", previewMsg.App.Iname, "device", device.ID)
								if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "displaying_app", previewMsg.App.Iname); err != nil {
									slog.Error("Failed to update displaying_app on preview", "device", device.ID, "error", err)
								}
								if user != nil {
									s.notifyDashboard(user.Username, WSEvent{Type: "image_updated", DeviceID: device.ID})
								}
							}
							if device.Info.ProtocolVersion != nil {
								waitingChunk = false
							}
						}
					case val := <-broadcastCh:
						reloaded, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", initialDevice.ID).First(ctx)
						if err != nil {
							slog.Error("Device gone", "id", initialDevice.ID)
							if timer != nil {
								timer.Stop()
							}
							return
						}
						device = reloaded

						switch v := val.(type) {
						case DeviceCommandMessage:
							if err := conn.WriteMessage(websocket.TextMessage, v.Payload); err != nil {
								slog.Error("Failed to write command to WS", "error", err)
								if timer != nil {
									timer.Stop()
								}
								return
							}
						case DevicePreviewMessage:
							pendingPreview = &v
							previewInterrupted = true
							waitingChunk = false
							sendImmediate = true
							if timer != nil {
								timer.Stop()
							}
						case *DevicePreviewMessage:
							pendingPreview = v
							previewInterrupted = true
							waitingChunk = false
							sendImmediate = true
							if timer != nil {
								timer.Stop()
							}
						case []byte:
							if len(v) > 0 {
								pendingImage = v
								previewInterrupted = true
								waitingChunk = false
								sendImmediate = true
								if timer != nil {
									timer.Stop()
								}
							}
						default:
							newBrightness := device.GetEffectiveBrightness()
							if newBrightness != lastSentBrightness {
								if err := conn.WriteJSON(map[string]int{"brightness": newBrightness}); err != nil {
									slog.Error("Failed to write brightness WS message", "error", err)
								}
								lastSentBrightness = newBrightness
							}
						}
					case <-timerC:
						waitingChunk = false
					}
				}
				if timer != nil {
					timer.Stop()
				}
				if previewInterrupted {
					break
				}
			}
			if previewInterrupted {
				continue
			}
			continue
		}

		// 1. Get Next Image
		var imgData []byte
		var app *data.App
		var err error

		if pendingImage != nil {
			imgData = pendingImage
			pendingImage = nil
		} else {
			imgData, app, err = s.GetNextAppImage(ctx, &device, user, baseURL)
			if err != nil {
				slog.Error("Failed to get next app", "error", err)

				// Wait for update or timeout before retrying
				timer := time.NewTimer(5 * time.Second)
				select {
				case val := <-broadcastCh:
					// Update available - reload device
					reloadedDevice, err := s.reloadDevice(initialDevice.ID)
					if err != nil {
						slog.Error("Device gone", "id", initialDevice.ID, "error", err)
						return
					}
					device = *reloadedDevice

					switch v := val.(type) {
					case DeviceCommandMessage:
						if err := conn.WriteMessage(websocket.TextMessage, v.Payload); err != nil {
							slog.Error("Failed to write command to WS", "error", err)
							timer.Stop()
							return
						}
					case DevicePreviewMessage:
						pendingPreview = &v
						sendImmediate = true
					case *DevicePreviewMessage:
						pendingPreview = v
						sendImmediate = true
					case []byte:
						if len(v) > 0 {
							pendingImage = v
							sendImmediate = true
						}
					}
				case <-timer.C:
					// Timeout - reload device just in case we missed something
					reloadedDevice, err := s.reloadDevice(initialDevice.ID)
					if err != nil {
						slog.Error("Device gone", "id", initialDevice.ID, "error", err)
						return
					}
					device = *reloadedDevice
				case <-stopCh:
					timer.Stop()
					return
				}
				timer.Stop()
				continue
			}
		}

		if pendingImage == nil && app != nil {
			chunks := s.getAppChunks(device.ID, app)
			if len(chunks) > 1 {
				// Defensive guard: if any chunk exceeds the device's maxChunkSize (e.g. from
				// an older cache or previous configuration), skip streaming multi-chunk to avoid crashing device
				maxChunkSize := s.GetMaxChunkSizeForDevice(&device)
				if maxChunkSize > 0 {
					oversized := false
					for _, ch := range chunks {
						if len(ch.WebPBytes) > maxChunkSize {
							oversized = true
							break
						}
					}
					if oversized {
						slog.Error("WS chunk exceeds device max buffer size, skipping multi-chunk app", "device", device.ID, "app", app.Iname, "max", maxChunkSize)
						skipTimer := time.NewTimer(5 * time.Second)
						select {
						case <-broadcastCh:
							reloadedDevice, err := s.reloadDevice(initialDevice.ID)
							if err != nil {
								slog.Error("Device gone", "id", initialDevice.ID, "error", err)
								skipTimer.Stop()
								return
							}
							device = *reloadedDevice
						case <-skipTimer.C:
						case <-stopCh:
							skipTimer.Stop()
							return
						case <-ctx.Done():
							skipTimer.Stop()
							return
						}
						skipTimer.Stop()
						continue
					}
				}

				// Only send volume if device has speaker capability and volume changed
				if device.HasSpeaker() {
					effectiveVolume := device.GetEffectiveVolume()
					if effectiveVolume != lastSentVolume {
						if err := conn.WriteJSON(map[string]int{"volume": effectiveVolume}); err != nil {
							slog.Error("Failed to write volume WS message", "error", err)
							return
						}
						lastSentVolume = effectiveVolume
					}
				}

				interrupted := false
				for chunkIdx, chunk := range chunks {
					select {
					case <-stopCh:
						return
					case <-ctx.Done():
						return
					default:
					}

					// Drain any stale unread ACKs before sending next chunk
					for {
						select {
						case <-ackCh:
						default:
							goto drained
						}
					}
				drained:

					chunkDwell := max(1, chunk.DurationMs/1000)
					if err := conn.WriteJSON(map[string]any{"dwell_secs": chunkDwell}); err != nil {
						slog.Error("Failed to write metadata WS message for chunk", "chunk", chunkIdx, "error", err)
						return
					}

					effectiveBrightness = device.GetEffectiveBrightness()
					if effectiveBrightness != lastSentBrightness {
						if err := conn.WriteJSON(map[string]int{"brightness": effectiveBrightness}); err != nil {
							slog.Error("Failed to write brightness WS message", "error", err)
							return
						}
						lastSentBrightness = effectiveBrightness
					}

					appName := "preview"
					if app != nil {
						appName = app.Iname
					}
					slog.Info("Streaming chunk to WebSocket",
						"app", appName,
						"device", device.ID,
						"chunk", chunkIdx,
						"total_chunks", len(chunks),
						"bytes", len(chunk.WebPBytes),
						"dwell_secs", chunkDwell,
						"duration_ms", chunk.DurationMs,
					)

					if err := conn.WriteMessage(websocket.BinaryMessage, chunk.WebPBytes); err != nil {
						slog.Error("Failed to write chunk binary message to WS", "chunk", chunkIdx, "error", err)
						return
					}

					if chunkIdx == 0 && sendImmediate {
						if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
							slog.Error("Failed to write immediate WS message", "chunk", chunkIdx, "error", err)
							return
						}
						sendImmediate = false
					} else if chunkIdx > 0 && device.Info.ProtocolVersion == nil {
						// For legacy devices without double buffering, force immediate display
						if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
							slog.Error("Failed to write immediate WS message for legacy chunk", "chunk", chunkIdx, "error", err)
							return
						}
					}

					chunkDuration := time.Duration(chunk.DurationMs) * time.Millisecond
					if chunkDuration <= 0 {
						chunkDuration = time.Duration(chunkDwell) * time.Second
					}

					var timer *time.Timer
					var timerC <-chan time.Time
					if device.Info.ProtocolVersion != nil {
						if chunkIdx == 0 {
							timer = time.NewTimer(time.Duration(maxDisplayingAckTimeoutSeconds) * time.Second)
						} else {
							prevDuration := time.Duration(chunks[chunkIdx-1].DurationMs) * time.Millisecond
							if prevDuration <= 0 {
								prevDuration = time.Duration(max(1, chunks[chunkIdx-1].DurationMs/1000)) * time.Second
							}
							timer = time.NewTimer(prevDuration + 10*time.Second)
						}
						timerC = timer.C
					} else {
						timer = time.NewTimer(chunkDuration)
						timerC = timer.C
					}

					waitingChunk := true
					for waitingChunk {
						select {
						case msg := <-ackCh:
							if msg.Displaying != nil || msg.Counter != nil {
								if chunkIdx == 0 && app != nil {
									slog.Debug("Received ACK for chunk 0, updating DisplayingApp", "app", app.Iname, "device", device.ID)
									if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "displaying_app", app.Iname); err != nil {
										slog.Error("Failed to update displaying_app", "device", device.ID, "error", err)
									}
									if user != nil {
										s.notifyDashboard(user.Username, WSEvent{Type: "image_updated", DeviceID: device.ID})
									}

									// Play notification sound on every display if configured
									if app.NotificationSound != nil && *app.NotificationSound != "" && *app.NotificationSound != "none" && app.NotificationSoundTrigger == "every_display" {
										if device.HasSpeaker() && !device.IsMuted() {
											baseURLToUse := s.GetDeviceBaseURL(&device)
											if baseURLToUse == "" || (!strings.Contains(baseURLToUse, ":") && strings.Contains(baseURL, ":")) {
												baseURLToUse = baseURL
											}
											payload := s.ResolveSoundPayload(&device, baseURLToUse, *app.NotificationSound)
											if payload != "" {
												if err := conn.WriteJSON(map[string]any{
													"type":   "sound",
													"sound":  payload,
													"volume": device.GetEffectiveVolume(),
												}); err != nil {
													slog.Error("Failed to write sound WS message on chunk display", "error", err)
												}
											}
										}
									}
									// Pre-render the next cycle in the background while the remaining chunks of this cycle play
									if app.Path != nil && s.isExternalAppPath(*app.Path) {
										appCopy := *app
										devCopy := device
										s.backgroundRenders.Go(func() {
											bgCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
											defer cancel()
											s.possiblyRender(bgCtx, &appCopy, &devCopy, user)
										})
									}
								} else if app != nil {
									slog.Debug("Received ACK for chunk", "app", app.Iname, "device", device.ID, "chunk", chunkIdx)
								}
								if device.Info.ProtocolVersion != nil {
									waitingChunk = false
								}
							}
						case val := <-broadcastCh:
							reloaded, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", initialDevice.ID).First(ctx)
							if err != nil {
								slog.Error("Device gone", "id", initialDevice.ID)
								if timer != nil {
									timer.Stop()
								}
								return
							}
							device = reloaded

							var isCommand bool
							var isImage bool
							var isPreview bool
							var cmdPayload []byte
							var pImgData []byte
							var pPreviewMsg *DevicePreviewMessage

							switch v := val.(type) {
							case DeviceCommandMessage:
								isCommand = true
								cmdPayload = v.Payload
							case DevicePreviewMessage:
								isPreview = true
								pPreviewMsg = &v
							case *DevicePreviewMessage:
								isPreview = true
								pPreviewMsg = v
							case []byte:
								if len(v) > 0 {
									isImage = true
									pImgData = v
								}
							}

							if isCommand {
								if err := conn.WriteMessage(websocket.TextMessage, cmdPayload); err != nil {
									slog.Error("Failed to write command to WS", "error", err)
									if timer != nil {
										timer.Stop()
									}
									return
								}
								continue
							}

							if isPreview {
								pendingPreview = pPreviewMsg
								interrupted = true
								waitingChunk = false
								sendImmediate = true
								if timer != nil {
									timer.Stop()
								}
							} else if isImage {
								pendingImage = pImgData
								interrupted = true
								waitingChunk = false
								sendImmediate = true
								if timer != nil {
									timer.Stop()
								}
							} else {
								newBrightness := device.GetEffectiveBrightness()
								if newBrightness != lastSentBrightness {
									if err := conn.WriteJSON(map[string]int{"brightness": newBrightness}); err != nil {
										slog.Error("Failed to write brightness WS message", "error", err)
										if timer != nil {
											timer.Stop()
										}
										return
									}
									lastSentBrightness = newBrightness
								}
							}
						case <-timerC:
							if device.Info.ProtocolVersion != nil {
								slog.Warn("Timed out waiting for displaying ACK on chunk", "chunk", chunkIdx, "device", device.ID)
							}
							waitingChunk = false
						case <-stopCh:
							if timer != nil {
								timer.Stop()
							}
							return
						case <-ctx.Done():
							if timer != nil {
								timer.Stop()
							}
							return
						}
					}

					if timer != nil {
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
					}

					if interrupted {
						break
					}
				}

				if interrupted {
					continue
				}
				continue
			}
		}

		dwell := device.GetEffectiveDwellTime(app)

		// Guard against oversized images for the device
		maxChunkSize := s.GetMaxChunkSizeForDevice(&device)
		if maxChunkSize > 0 && len(imgData) > maxChunkSize {
			slog.Error("WS image exceeds device max buffer size, skipping", "device", device.ID, "size", len(imgData), "max", maxChunkSize)
			continue
		}

		if err := conn.WriteJSON(map[string]any{"dwell_secs": dwell}); err != nil {
			slog.Error("Failed to write metadata WS message", "error", err)
			return
		}

		// Only send brightness if changed
		if effectiveBrightness != lastSentBrightness {
			if err := conn.WriteJSON(map[string]int{"brightness": effectiveBrightness}); err != nil {
				slog.Error("Failed to write brightness WS message", "error", err)
				return
			}
			lastSentBrightness = effectiveBrightness
		}

		// Only send volume if device has speaker capability and volume changed
		if device.HasSpeaker() {
			effectiveVolume := device.GetEffectiveVolume()
			if effectiveVolume != lastSentVolume {
				if err := conn.WriteJSON(map[string]int{"volume": effectiveVolume}); err != nil {
					slog.Error("Failed to write volume WS message", "error", err)
					return
				}
				lastSentVolume = effectiveVolume
			}
		}

		if err := conn.WriteMessage(websocket.BinaryMessage, imgData); err != nil {
			return
		}

		if sendImmediate {
			if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
				slog.Error("Failed to write immediate WS message", "error", err)
				return
			}
			sendImmediate = false
		}

		// 3. Wait for displaying ACK, safety/legacy timeout, or interrupt.
		// v1+ firmware sends displaying when the image is on screen (including long
		// animations). We wait for that ACK, with a long safety timeout if it never arrives.
		var timer *time.Timer
		var timerC <-chan time.Time
		if device.Info.ProtocolVersion != nil {
			timer = time.NewTimer(time.Duration(maxDisplayingAckTimeoutSeconds) * time.Second)
			timerC = timer.C
		} else {
			timer = time.NewTimer(time.Duration(dwell) * time.Second)
			timerC = timer.C
		}

		interrupted := false
		waiting := true

		for waiting {
			select {
			case msg := <-ackCh:
				// Received ACK
				if msg.Displaying != nil || msg.Counter != nil {
					// The firmware sends a sequential counter (1, 2, 3...) in the 'displaying' field,
					// not the App ID we sent. Since we can't verify which specific app is displaying,
					// we accept any ACK as confirmation that the device received and processed our message.
					waiting = false

					// Update DisplayingApp confirmation in DB
					if app != nil {
						// Ensure app metadata reflects the latest configuration from device.Apps
						for i := range device.Apps {
							if device.Apps[i].ID == app.ID {
								app = device.Apps[i]
								break
							}
						}
						slog.Debug("Received ACK, updating DisplayingApp", "app", app.Iname, "device", device.ID)
						// Only now do we update the database that the device is truly displaying this app.
						if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "displaying_app", app.Iname); err != nil {
							slog.Error("Failed to update displaying_app", "device", device.ID, "error", err)
						}
						// Notify Dashboard
						s.notifyDashboard(user.Username, WSEvent{Type: "image_updated", DeviceID: device.ID})

						// Play notification sound on every display if configured
						if app.NotificationSound != nil && *app.NotificationSound != "" && *app.NotificationSound != "none" && app.NotificationSoundTrigger == "every_display" {
							if device.HasSpeaker() && !device.IsMuted() {
								baseURLToUse := s.GetDeviceBaseURL(&device)
								if baseURLToUse == "" || (!strings.Contains(baseURLToUse, ":") && strings.Contains(baseURL, ":")) {
									baseURLToUse = baseURL
								}
								payload := s.ResolveSoundPayload(&device, baseURLToUse, *app.NotificationSound)
								if payload != "" {
									if err := conn.WriteJSON(map[string]any{
										"type":   "sound",
										"sound":  payload,
										"volume": device.GetEffectiveVolume(),
									}); err != nil {
										slog.Error("Failed to write sound WS message on display", "error", err)
									}
								}
							}
						}
					} else {
						slog.Debug("Received ACK for default or pushed image (no app context)", "device", device.ID)
					}
				}
			case val := <-broadcastCh:
				// Update available (Reload device first)
				reloaded, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", initialDevice.ID).First(ctx)
				if err != nil {
					slog.Error("Device gone", "id", initialDevice.ID)
					return
				}
				device = reloaded
				if app != nil {
					for i := range device.Apps {
						if device.Apps[i].ID == app.ID {
							app = device.Apps[i]
							break
						}
					}
				}

				var isCommand bool
				var isImage bool
				var isPreview bool
				var cmdPayload []byte
				var imgData []byte
				var pPreviewMsg *DevicePreviewMessage

				switch v := val.(type) {
				case DeviceCommandMessage:
					isCommand = true
					cmdPayload = v.Payload
				case DevicePreviewMessage:
					isPreview = true
					pPreviewMsg = &v
				case *DevicePreviewMessage:
					isPreview = true
					pPreviewMsg = v
				case []byte:
					if len(v) > 0 {
						isImage = true
						imgData = v
					}
				}

				if isCommand {
					// It's a command, send it directly as JSON (TextMessage)
					if err := conn.WriteMessage(websocket.TextMessage, cmdPayload); err != nil {
						slog.Error("Failed to write command to WS", "error", err)
						return
					}
					// Don't interrupt the current app for settings changes, unless it's a reboot (which the device handles)
					continue
				}

				if isPreview {
					// Preview: Interrupt and stream preview chunks
					pendingPreview = pPreviewMsg
					interrupted = true
					waiting = false
					sendImmediate = true
				} else if isImage {
					// Pushed Image: Interrupt and send
					pendingImage = imgData
					interrupted = true
					waiting = false
					sendImmediate = true
				} else {
					// State Change: Update Brightness and Volume immediately, but don't interrupt current app
					newBrightness := device.GetEffectiveBrightness()

					if newBrightness != lastSentBrightness {
						if err := conn.WriteJSON(map[string]int{"brightness": newBrightness}); err != nil {
							slog.Error("Failed to write brightness WS message", "error", err)
							return
						}
						lastSentBrightness = newBrightness
					}

					if device.HasSpeaker() {
						newVolume := device.GetEffectiveVolume()
						if newVolume != lastSentVolume {
							if err := conn.WriteJSON(map[string]int{"volume": newVolume}); err != nil {
								slog.Error("Failed to write volume WS message", "error", err)
								return
							}
							lastSentVolume = newVolume
						}
					}
				}
			case <-timerC:
				if device.Info.ProtocolVersion != nil {
					appIname := ""
					if app != nil {
						appIname = app.Iname
					}
					slog.Warn(
						"Timed out waiting for displaying ACK, advancing rotation",
						"device", device.ID,
						"app", appIname,
						"timeout_secs", maxDisplayingAckTimeoutSeconds,
					)
				}
				waiting = false
			case <-stopCh:
				if timer != nil {
					timer.Stop()
				}
				return
			}
		}
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		if interrupted {
			continue
		}
	}
}

func (s *Server) handleDashboardWS(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	username := user.Username

	conn, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Dashboard WS upgrade failed", "error", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close Dashboard WS connection", "error", err)
		}
	}()

	slog.Debug("Dashboard WS Connected", "username", username)

	// Subscribe to user-specific updates
	ch := s.Broadcaster.Subscribe("user:" + username)
	defer s.Broadcaster.Unsubscribe("user:"+username, ch)

	done := make(chan struct{})

	// Read loop (handle ping/pong/close)
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Info("Dashboard WS read error, disconnecting", "username", username, "error", err)
				}
				return
			}
		}
	}()

	// Write loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case val := <-ch:
			var data []byte
			if b, ok := val.([]byte); ok {
				data = b
			}

			// Forward the event data (JSON) to the client
			if len(data) == 0 {
				// Fallback for legacy calls sending nil: trigger generic refresh
				data = []byte(`{"type": "refresh"}`)
			}
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				slog.Error("Failed to write message to Dashboard WS", "username", username, "error", err)
				return
			}
		case <-ticker.C:
			// Keep-alive ping
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Error("Failed to send Dashboard WS ping", "username", username, "error", err)
				return
			}
		}
	}
}

func (s *Server) SetupWebsocketRoutes() {
	s.Router.HandleFunc("GET /{id}/ws", s.handleWS)
	s.Router.HandleFunc("GET /ws", s.RequireLogin(s.handleDashboardWS))
}

func (s *Server) reloadDevice(deviceID string) (*data.Device, error) {
	device, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("device not found: %s", deviceID)
		}
		return nil, fmt.Errorf("reload device: %w", err)
	}
	return &device, nil
}
