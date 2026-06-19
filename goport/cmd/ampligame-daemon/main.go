// Command ampligame-daemon is the reference/test application for the mirajazz
// Go port. It connects to a Fifine Ampligame D6 (15-key Mirabox/Ajazz clone,
// protocol version 3), reads physical key events through the ported library,
// and dispatches an outbound webhook per press to a local k3s ingress.
//
// It is the Go-port-spec's test app, rewritten to drive the real ported library
// instead of parsing raw HID bytes inline. The key difference from the spec's
// throwaway parser: button events come from mirajazz's protocol-correct reader
// (ACK-prefixed reports, key at byte 9 / state at byte 10), not a naive
// buf[1]/buf[2] read, so it matches the reverse-engineered Rust protocol.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bto-labs/mirajazz-go/internal/mirajazz"
	"github.com/bto-labs/mirajazz-go/internal/mirajazz/hidapi"
)

const (
	// FifineVendorID identifies the device as a Mirabox/clone.
	FifineVendorID uint16 = 0x3142
	// KeyCount is the number of keys on the Ampligame D6.
	KeyCount = 15
	// EncoderCount is zero (no knobs on the D6).
	EncoderCount = 0
	// ProtocolVersion 3: 1024-byte packets, both keypress states, JPEG images.
	ProtocolVersion = 3
)

// query matches the D6 by vendor id + stream-controller usage page. The product
// id varies by batch, so it is left as ProductIDAny. The usage page/id (65440,1)
// match the values the upstream Rust examples use for these devices.
var query = mirajazz.NewDeviceQuery(65440, 1, FifineVendorID, mirajazz.ProductIDAny)

// ButtonEvent is a normalized press/release from the device.
type ButtonEvent struct {
	ButtonID int
	IsDown   bool
}

// WebhookPayload is the outbound message to the cluster ingress.
type WebhookPayload struct {
	Device    string    `json:"device"`
	Button    int       `json:"button_id"`
	Event     string    `json:"event_state"`
	Timestamp time.Time `json:"timestamp"`
}

func main() {
	log.Println("Initializing Fifine Ampligame Port Engine...")

	if err := hidapi.Init(); err != nil {
		log.Fatalf("Failed to initialize HID API: %v", err)
	}
	defer hidapi.Exit()

	// Open the first matching device and wrap it in the ported library.
	conn, info, err := hidapi.OpenFirst([]mirajazz.DeviceQuery{query})
	if err != nil {
		log.Fatalf("Device connection failed. Ensure udev rules are loaded. Error: %v", err)
	}

	device, err := mirajazz.Connect(conn, info, ProtocolVersion, KeyCount, EncoderCount)
	if err != nil {
		log.Fatalf("Failed to initialize device: %v", err)
	}
	defer device.Close()

	fw := "unknown"
	if device.FirmwareVersion != nil {
		fw = *device.FirmwareVersion
	}
	log.Printf("Hardware handshake successful. Fifine D6 connected (serial %s, fw %s).",
		device.SerialNumber, fw)

	// Wake the screen and start from a known-clean state.
	if err := device.SetBrightness(50); err != nil {
		log.Printf("Warning: failed to set brightness: %v", err)
	}
	if err := device.ClearAllButtonImages(); err != nil {
		log.Printf("Warning: failed to clear screen: %v", err)
	}

	eventChan := make(chan ButtonEvent, 10)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Background reader loop: the ported DeviceStateReader turns raw reports
	// into discrete press/release updates.
	go readDeviceLoop(device, eventChan)

	for {
		select {
		case event := <-eventChan:
			handleButtonAction(event)
		case sig := <-sigChan:
			log.Printf("System signal [%v] captured. Powering down daemon safely...", sig)
			if err := device.Shutdown(); err != nil {
				log.Printf("Warning: shutdown failed: %v", err)
			}
			return
		}
	}
}

// processInput maps a raw device (key, state) into a full 15-button snapshot,
// as the mirajazz reader expects. The D6 reports the changed key index in
// report byte 9 and its state in byte 10; the reader diffs successive snapshots
// to emit ButtonDown/ButtonUp.
//
// NOTE: some clones report a 1-based key index (the akp153 example subtracts 1).
// If your D6's events look off-by-one, adjust the index here.
func processInput(key, state byte) (mirajazz.DeviceInput, error) {
	buttons := make([]bool, KeyCount)
	if int(key) < KeyCount {
		buttons[key] = state == 0x01
	}
	return mirajazz.ButtonStateChange(buttons), nil
}

// readDeviceLoop reads updates and forwards button transitions to eventChan.
func readDeviceLoop(device *mirajazz.Device, ch chan<- ButtonEvent) {
	reader := device.GetReader(processInput)
	for {
		updates, err := reader.Read(nil)
		if err != nil {
			log.Printf("Read error encountered on HID pipeline: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, u := range updates {
			switch u.Kind {
			case mirajazz.UpdateButtonDown:
				ch <- ButtonEvent{ButtonID: int(u.Index), IsDown: true}
			case mirajazz.UpdateButtonUp:
				ch <- ButtonEvent{ButtonID: int(u.Index), IsDown: false}
			}
		}
	}
}

// handleButtonAction logs the transition and, on press, dispatches a webhook.
func handleButtonAction(event ButtonEvent) {
	stateStr := "RELEASED"
	if event.IsDown {
		stateStr = "PRESSED"
	}
	log.Printf("[Hardware Event] Button #%d state changed to: %s", event.ButtonID, stateStr)

	// Suppress execution on release to avoid double-triggering.
	if !event.IsDown {
		return
	}

	var targetURL string
	switch event.ButtonID {
	case 0:
		targetURL = "https://ingress.homelab.local/mesh-six/api/v1/agent/worker"
	case 1:
		targetURL = "https://ingress.homelab.local/bto-fs/api/v1/cluster/sync"
	default:
		targetURL = "https://ingress.homelab.local/cluster-events/generic"
	}

	go dispatchWebhook(targetURL, event.ButtonID, stateStr)
}

// dispatchWebhook posts the event to a cluster ingress endpoint.
func dispatchWebhook(url string, buttonID int, state string) {
	client := &http.Client{Timeout: 3 * time.Second}

	payload := WebhookPayload{
		Device:    "fifine_ampligame_d6",
		Button:    buttonID,
		Event:     state,
		Timestamp: time.Now().UTC(),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal payload for JSON processing: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("[Network Error] Failed to build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Network Error] Outbound hook delivery failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[Routing Success] Webhook captured by cluster ingress for button %d", buttonID)
	} else {
		log.Printf("[Routing Alert] Ingress returned non-2xx status: %d", resp.StatusCode)
	}
}
