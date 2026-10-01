package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderAddAppFor renders the Add App page for a device of the given type and
// returns the HTML.
func renderAddAppFor(t *testing.T, deviceType data.DeviceType) string {
	t.Helper()

	s := newTestServerAPI(t)

	// Two apps that differ only in what they declare, so the attributes the
	// filter reads are present on a real card rather than asserted in the
	// abstract.
	s.systemAppsCacheMutex.Lock()
	s.systemAppsCache = []apps.AppMetadata{
		{Manifest: apps.Manifest{
			ID: "squarely", Name: "Squarely", PackageName: "squarely",
			FileName: "squarely.star", Author: "tester", Category: "utilities",
			Supports2x: true, Supports64x64: true,
		}},
		{Manifest: apps.Manifest{
			ID: "flatly", Name: "Flatly", PackageName: "flatly",
			FileName: "flatly.star", Author: "tester", Category: "utilities",
		}},
	}
	s.systemAppsCacheMutex.Unlock()

	var user data.User
	require.NoError(t, s.DB.First(&user, "username = ?", "testuser").Error)
	var device data.Device
	require.NoError(t, s.DB.First(&device, "id = ?", "testdevice").Error)
	device.Type = deviceType

	req := httptest.NewRequest(http.MethodGet, "/devices/testdevice/addapp", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleAddAppGet(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	return rr.Body.String()
}

// The filter is only worth offering on a panel that some apps do not fill. On
// a classic 64x32 panel every app fits, so the control is not rendered at all
// rather than rendered as a no-op.
func TestAddAppDisplayFilterControl(t *testing.T) {
	tests := []struct {
		name           string
		deviceType     data.DeviceType
		wantControl    bool
		wantCapability string
	}{
		{"square offers a 64x64 filter", data.DeviceMatrixPortalSquare, true, "64x64"},
		{"wide offers a 2x filter", data.DeviceRaspberryPiWide, true, "2x"},
		{"classic offers no filter", data.DeviceRaspberryPi, false, ""},
		{"tidbyt offers no filter", data.DeviceTidbytGen1, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := renderAddAppFor(t, tt.deviceType)

			if !tt.wantControl {
				assert.NotContains(t, body, "fits_display_",
					"no app needs a capability to fill this panel, so no control should render")
				return
			}

			require.Contains(t, body, `id="fits_display_system_search"`)
			assert.Contains(t, body, `data-capability="`+tt.wantCapability+`"`)
			assert.Contains(t, body, "toggleFitsDisplay(")
		})
	}
}

// A square panel and a wide panel are both 64 tall, so the control has to be
// driven by shape rather than height.
func TestAddAppDisplayFilterTellsSquareFromWide(t *testing.T) {
	square := renderAddAppFor(t, data.DeviceMatrixPortalSquare)
	wide := renderAddAppFor(t, data.DeviceRaspberryPiWide)

	assert.Contains(t, square, `data-capability="64x64"`)
	assert.NotContains(t, square, `data-capability="2x"`)

	assert.Contains(t, wide, `data-capability="2x"`)
	assert.NotContains(t, wide, `data-capability="64x64"`)
}

// The filter reads these off each card; without them it would hide everything.
func TestAddAppCardsCarryCapabilityData(t *testing.T) {
	body := renderAddAppFor(t, data.DeviceMatrixPortalSquare)

	require.Contains(t, body, `data-name="Squarely"`)
	require.Contains(t, body, `data-name="Flatly"`)

	// The square-capable app declares both; the other declares neither. The
	// filter hides any card whose attribute is not exactly "true", so the
	// false case has to be rendered rather than omitted.
	assert.Contains(t, body, `data-supports-64x64="true"`)
	assert.Contains(t, body, `data-supports-64x64="false"`)
	assert.Contains(t, body, `data-supports-2x="true"`)
	assert.Contains(t, body, `data-supports-2x="false"`)

	// And the badge the control's chip echoes is on the capable card.
	assert.Contains(t, body, "supports-64x64-badge")
}
