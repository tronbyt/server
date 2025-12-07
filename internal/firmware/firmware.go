package firmware

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

const (
	PlaceholderSSID     = "XplaceholderWIFISSID____________"
	PlaceholderPassword = "XplaceholderWIFIPASSWORD________________________________________"
	PlaceholderURL      = "XplaceholderREMOTEURL___________________________________________________________________________________________________________"
)

func Generate(dataDir string, deviceType string, ssid, password, url string, swapColors bool) ([]byte, error) {
	filename := getFirmwareFilename(deviceType, swapColors)
	path := filepath.Join(dataDir, "firmware", filename)

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("firmware file not found: %s", path)
	}

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
			// Try without null terminator if not found?
			// Python script enforced + \x00. We stick to it.
			return nil, fmt.Errorf("placeholder %s not found", oldStr)
		}

		// Create replacement: new string + null + padding nulls
		// Length must match `search` length (oldStr + \x00)
		replacement := make([]byte, len(search))
		copy(replacement, []byte(newStr))
		// Remaining bytes are 0 by default (padding)

		// Overwrite content
		copy(content[idx:], replacement)
	}

	// Correct Checksum and Digest
	return updateFirmwareData(content)
}

func getFirmwareFilename(deviceType string, swapColors bool) string {
	switch deviceType {
	case "tidbyt_gen2":
		return "tidbyt-gen2.bin"
	case "pixoticker":
		return "pixoticker.bin"
	case "tronbyt_s3":
		return "tronbyt-S3.bin"
	case "tronbyt_s3_wide":
		return "tronbyt-s3-wide.bin"
	case "matrixportal_s3":
		return "matrixportal-s3.bin"
	case "matrixportal_s3_waveshare":
		return "matrixportal-s3-waveshare.bin"
	default:
		if swapColors {
			return "tidbyt-gen1_swap.bin"
		}
		return "tidbyt-gen1.bin"
	}
}

func updateFirmwareData(data []byte) ([]byte, error) {
	// Image format: [Data ...][Checksum 1B][SHA256 32B]
	// Total length - 33 is the data length.

	if len(data) < 33 {
		return nil, fmt.Errorf("firmware binary too short")
	}

	dataLen := len(data) - 33

	// 1. Calculate Checksum (XOR sum of data + 0xEF)
	checksum := byte(0xEF)
	for i := 0; i < dataLen; i++ {
		checksum ^= data[i]
	}

	// Write checksum
	data[len(data)-33] = checksum

	// 2. Calculate SHA256 (Hash of Data + Checksum)
	// Hash content is everything up to the digest.
	hashContent := data[:len(data)-32]
	hash := sha256.Sum256(hashContent)

	// Write hash
	copy(data[len(data)-32:], hash[:])

	return data, nil
}
