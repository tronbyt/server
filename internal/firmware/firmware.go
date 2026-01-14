package firmware

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"tronbyt-server/internal/data"
)

const (
	PlaceholderSSID     = "XplaceholderWIFISSID____________"
	PlaceholderPassword = "XplaceholderWIFIPASSWORD________________________________________"
	PlaceholderURL      = "XplaceholderREMOTEURL___________________________________________________________________________________________________________"

	// MergedAppOffset is the offset where the app binary starts in a merged firmware image.
	// Merged binaries contain: bootloader (0x0/0x1000) + partition table (0x8000) + app (0x10000).
	MergedAppOffset = 0x10000
)

func Generate(dataDir string, deviceType data.DeviceType, ssid, password, url string, swapColors bool) ([]byte, error) {
	filename := deviceType.FirmwareFilename(swapColors)
	path := filepath.Join(dataDir, "firmware", filename)

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("firmware file not found: %s", path)
	}

	if len(content) < 33 {
		return nil, fmt.Errorf("firmware binary too short")
	}

	// Track checksum delta from placeholder replacements
	// ESP32 image checksum is complex (header + segment data only), but we can
	// calculate the delta from our changes and apply it to the original checksum.
	var checksumDelta byte

	// Replacements
	replacements := map[string]string{
		PlaceholderSSID:     ssid,
		PlaceholderPassword: password,
		PlaceholderURL:      url,
	}

	for oldStr, newStr := range replacements {
		if len(newStr) > len(oldStr) {
			return nil, fmt.Errorf("value for %s too long", oldStr)
		}

		// Search for placeholder + null terminator
		search := []byte(oldStr + "\x00")
		idx := bytes.Index(content, search)
		if idx == -1 {
			return nil, fmt.Errorf("placeholder %s not found", oldStr)
		}

		// Create replacement: new string + null + padding nulls
		replacement := make([]byte, len(search))
		copy(replacement, []byte(newStr))

		// Calculate checksum delta: XOR of old bytes XOR new bytes
		// This captures the change in checksum caused by this replacement
		for i := range len(search) {
			checksumDelta ^= content[idx+i] ^ replacement[i]
		}

		// Overwrite content
		copy(content[idx:], replacement)
	}

	// Apply checksum delta to original checksum
	checksumPos := len(content) - 33
	content[checksumPos] ^= checksumDelta

	// Recalculate SHA256 (this IS a simple hash of everything except last 32 bytes)
	hashContent := content[:len(content)-32]
	hash := sha256.Sum256(hashContent)
	copy(content[len(content)-32:], hash[:])

	return content, nil
}

// GenerateMerged generates a merged firmware binary (bootloader + partition + app) with
// injected WiFi credentials and URL. The merged binary is flashable at address 0x0.
// It works by:
// 1. Generating the injected firmware.bin using Generate()
// 2. Reading the preamble (bootloader + partition table) from the merged binary
// 3. Combining preamble + injected firmware.
func GenerateMerged(dataDir string, deviceType data.DeviceType, ssid, password, url string, swapColors bool) ([]byte, error) {
	// Generate the injected firmware binary
	firmwareData, err := Generate(dataDir, deviceType, ssid, password, url, swapColors)
	if err != nil {
		return nil, fmt.Errorf("failed to generate firmware: %w", err)
	}

	// Read the preamble from the merged binary (bootloader + partition table, first 0x10000 bytes)
	mergedFilename := deviceType.MergedFilename(swapColors)
	if mergedFilename == "" {
		return nil, fmt.Errorf("merged firmware not available for device type %s", deviceType.String())
	}
	mergedPath := filepath.Join(dataDir, "firmware", mergedFilename)

	mergedContent, err := os.ReadFile(mergedPath)
	if err != nil {
		return nil, fmt.Errorf("merged firmware file not found (needed for bootloader/partition): %s", mergedPath)
	}

	if len(mergedContent) < MergedAppOffset {
		return nil, fmt.Errorf("merged firmware file too short to contain preamble")
	}

	// Extract the preamble (first 0x10000 bytes containing bootloader + partition table)
	preamble := mergedContent[:MergedAppOffset]

	// Combine preamble + injected firmware
	result := make([]byte, len(preamble)+len(firmwareData))
	copy(result, preamble)
	copy(result[MergedAppOffset:], firmwareData)

	return result, nil
}
