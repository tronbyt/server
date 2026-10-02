package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/connections"
	"tronbyt-server/internal/data"

	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

// Device authorization grant (RFC 8628). The user clicks Connect, the
// server asks the provider for a short code, and that code is shown both
// in the browser and *on the matrix itself*. The user approves on their
// phone; a background goroutine polls the token endpoint and stores the
// connection when it lands. No redirect URI, no callback host — which is
// what makes this work on a device sitting on a LAN with no public name.

// deviceFlowStatus is the state a pending flow reports to the page.
type deviceFlowStatus string

const (
	deviceFlowPending   deviceFlowStatus = "pending"
	deviceFlowConnected deviceFlowStatus = "connected"
	deviceFlowDenied    deviceFlowStatus = "denied"
	deviceFlowExpired   deviceFlowStatus = "expired"
	deviceFlowFailed    deviceFlowStatus = "failed"
)

// deviceFlow is one in-flight device authorization. These live in memory
// only: a flow is worth less than the code's lifetime, and a server
// restart mid-flow just means clicking Connect again.
type deviceFlow struct {
	ID       string
	Provider string
	Username string

	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               time.Time

	mu     sync.Mutex
	status deviceFlowStatus
	label  string // "Connected as <name>" once done
	errMsg string
}

func (f *deviceFlow) snapshot() (deviceFlowStatus, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.label, f.errMsg
}

func (f *deviceFlow) set(status deviceFlowStatus, label, errMsg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.label, f.errMsg = status, label, errMsg
}

// deviceFlowTTL bounds how long a finished flow stays readable by the
// status endpoint before it's swept.
const deviceFlowTTL = 20 * time.Minute

// putDeviceFlow stores a flow and opportunistically sweeps stale ones.
func (s *Server) putDeviceFlow(f *deviceFlow) {
	s.deviceFlowsMu.Lock()
	defer s.deviceFlowsMu.Unlock()
	if s.deviceFlows == nil {
		s.deviceFlows = make(map[string]*deviceFlow)
	}
	cutoff := time.Now().Add(-deviceFlowTTL)
	for id, existing := range s.deviceFlows {
		if existing.ExpiresAt.Before(cutoff) {
			delete(s.deviceFlows, id)
		}
	}
	s.deviceFlows[f.ID] = f
}

func (s *Server) getDeviceFlow(id string) (*deviceFlow, bool) {
	s.deviceFlowsMu.RLock()
	defer s.deviceFlowsMu.RUnlock()
	f, ok := s.deviceFlows[id]
	return f, ok
}

// handleDeviceFlowStart kicks off a device authorization and renders the
// waiting page.
//
//	GET /connections/device/{provider}?return_to=...
func (s *Server) handleDeviceFlowStart(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	providerName := strings.ToLower(r.PathValue("provider"))

	provider, ok := s.ConnectionsRegistry.Get(providerName)
	if !ok {
		http.Error(w, "Unknown provider", http.StatusNotFound)
		return
	}
	if !s.Connections.DeviceFlowAvailable(provider) {
		http.Error(w, "This provider is not configured for device login on this server. Ask your admin to set "+
			strings.ToUpper(provider.Name)+"_CLIENT_ID.", http.StatusServiceUnavailable)
		return
	}

	_, da, err := s.Connections.StartDeviceAuth(r.Context(), provider.Name, nil)
	if err != nil {
		slog.Error("Device flow: start failed", "provider", provider.Name, "user", user.Username, "error", err)
		s.flashAndRedirect(w, r, "Could not start device login: "+err.Error(), "/connections", http.StatusSeeOther)
		return
	}

	id, err := generateSecureTokenEncoded(16)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	flow := &deviceFlow{
		ID:                      id,
		Provider:                provider.Name,
		Username:                user.Username,
		UserCode:                da.UserCode,
		VerificationURI:         da.VerificationURI,
		VerificationURIComplete: da.VerificationURIComplete,
		ExpiresAt:               da.Expiry,
		status:                  deviceFlowPending,
	}
	s.putDeviceFlow(flow)

	// Show the code on the user's displays, and start polling. Both
	// outlive this request: the user may close the tab and still approve.
	s.pushDeviceCodeToDisplays(r.Context(), user, provider.DisplayName, da.UserCode, da.VerificationURI)
	go s.pollDeviceFlow(flow, da)

	returnTo := r.URL.Query().Get("return_to")
	if !isSafeReturnTo(returnTo) {
		returnTo = "/connections"
	}

	s.renderTemplate(w, r, "deviceconnect", TemplateData{
		DeviceFlow: &DeviceFlowView{
			ID:                      flow.ID,
			ProviderName:            provider.Name,
			ProviderDisplayName:     provider.DisplayName,
			UserCode:                flow.UserCode,
			VerificationURI:         flow.VerificationURI,
			VerificationURIComplete: flow.VerificationURIComplete,
			ExpiresInSeconds:        int(time.Until(flow.ExpiresAt).Seconds()),
			ShownOnDisplays:         len(user.Devices),
			ReturnTo:                returnTo,
		},
	})
}

// pollDeviceFlow blocks on the provider's token endpoint until the user
// approves, denies, or the code expires, then records the outcome and
// clears the code from the displays.
func (s *Server) pollDeviceFlow(flow *deviceFlow, da *oauth2.DeviceAuthResponse) {
	// Bound the poll by the code's own lifetime; DeviceAccessToken honors
	// the deadline, and this guarantees the goroutine always exits.
	deadline := flow.ExpiresAt
	if deadline.IsZero() {
		deadline = time.Now().Add(15 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline.Add(5*time.Second))
	defer cancel()

	conn, err := s.Connections.CompleteDeviceAuth(ctx, flow.Provider, flow.Username, da, nil)

	// Whatever happened, the code on the display is now stale.
	defer func() {
		user, uerr := s.loadUserWithDevices(context.Background(), flow.Username)
		if uerr == nil {
			s.clearDeviceCodeFromDisplays(user)
		}
	}()

	if err != nil {
		status, msg := classifyDeviceFlowError(err)
		slog.Warn("Device flow: not completed", "provider", flow.Provider, "user", flow.Username, "status", status, "error", err)
		flow.set(status, "", msg)
		return
	}

	label := conn.DisplayName
	if label == "" {
		label = conn.ExternalID
	}
	slog.Info("Device flow: connection established", "provider", conn.Provider, "user", flow.Username, "external_id", conn.ExternalID)
	flow.set(deviceFlowConnected, label, "")
}

// classifyDeviceFlowError turns a polling failure into a status the page
// can show. RFC 8628 defines the terminal error codes; providers that
// return non-standard shapes fall through to a generic failure.
func classifyDeviceFlowError(err error) (deviceFlowStatus, string) {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		switch retrieve.ErrorCode {
		// "authorization_declined" is Microsoft's spelling of access_denied.
		case "access_denied", "authorization_declined":
			return deviceFlowDenied, "You declined the request."
		case "expired_token", "bad_verification_code":
			return deviceFlowExpired, "The code expired. Start again to get a new one."
		case "device_flow_disabled":
			return deviceFlowFailed, "Device login is not enabled on the server's OAuth app for this provider."
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return deviceFlowExpired, "The code expired. Start again to get a new one."
	}
	if errors.Is(err, connections.ErrProviderDisabled) {
		return deviceFlowFailed, "This provider is not configured on the server."
	}
	return deviceFlowFailed, "Could not complete the connection."
}

// handleDeviceFlowStatus is polled by the waiting page.
//
//	GET /connections/device/status/{id}
func (s *Server) handleDeviceFlowStatus(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	flow, ok := s.getDeviceFlow(r.PathValue("id"))
	// A flow belongs to the user who started it; anyone else gets the same
	// answer as for an unknown id.
	if !ok || flow.Username != user.Username {
		http.Error(w, "Unknown device login", http.StatusNotFound)
		return
	}

	status, label, errMsg := flow.snapshot()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status": string(status),
		"label":  label,
		"error":  errMsg,
	}); err != nil {
		slog.Error("Device flow: failed to write status", "error", err)
	}
}

// loadUserWithDevices re-reads a user (with devices) outside a request,
// for the background poller.
func (s *Server) loadUserWithDevices(ctx context.Context, username string) (*data.User, error) {
	user, err := gorm.G[data.User](s.DB).
		Preload("Devices", nil).
		Where("username = ?", username).
		First(ctx)
	if err != nil {
		return nil, err
	}
	return &user, nil
}
