package mirajazz

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// All multi-byte commands begin with this "CRT" preamble (0x43 0x52 0x54)
// preceded by the 0x00 report id. The bytes that follow are short ASCII opcodes
// such as DIS, LIG, BAT, CLE, STP, HAN, MOD, decoded inline below.
//
// The numbers in the command buffers are kept as raw hex (matching the Rust
// source) rather than character literals so the port diffs cleanly against the
// upstream crate.

// Device is a connected stream-controller. It is the port of the Rust `Device`
// struct and owns the HID connection plus the pending-image cache.
//
// A Device is safe for concurrent use: writes are serialized by writeMu and
// reads (performed by a DeviceStateReader) by readMu, mirroring async-hid's
// split reader/writer halves.
type Device struct {
	// VID is the vendor id of the device.
	VID uint16
	// PID is the product id of the device.
	PID uint16
	// SerialNumber is the resolved serial (hardcoded for protocol v1 devices).
	SerialNumber string
	// FirmwareVersion is best-effort; nil if it could not be read.
	FirmwareVersion *string

	protocolVersion            int
	supportsBothKeypressStates bool
	supportsBothEncoderStates  bool
	keyCount                   int
	encoderCount               int
	packetSize                 int

	conn    Conn
	writeMu sync.Mutex
	readMu  sync.Mutex

	cacheMu    sync.Mutex
	imageCache map[byte][]byte

	initialized atomic.Bool
}

// Connect wraps an already-open [Conn] in a [Device] for the given protocol
// version, key count and encoder count. The caller (typically the hidapi
// subpackage) is responsible for enumerating and opening the handle and for
// supplying its [DeviceInfo].
//
// Port note: the Rust connect() opens the device itself via async-hid. Here the
// open is the hidapi layer's job so this package stays hardware-free; everything
// else (serial resolution, the firmware read, the protocol-version 0 fallback)
// is ported faithfully.
func Connect(conn Conn, info DeviceInfo, protocolVersion, keyCount, encoderCount int) (*Device, error) {
	// Rust uses assert!; a library returns an error instead of panicking.
	if protocolVersion < 1 || protocolVersion > 3 {
		return nil, ErrInvalidProtocolVersion
	}

	// Best-effort firmware read. The Rust crate propagates this error, but a
	// feature-report failure should not prevent talking to the device, so the
	// port degrades to a nil firmware version (documented divergence).
	firmware := readFirmwareVersion(conn)

	// Serial number resolution, matching the Rust match expression:
	//   - protocol v1 devices have a broken/absent serial -> hardcode it.
	//   - v2+ must report a serial, otherwise the device is invalid.
	var serial string
	switch {
	case protocolVersion == 1:
		serial = "355499441494"
	case info.SerialNumber != "":
		serial = info.SerialNumber
	default:
		return nil, ErrInvalidDevice
	}

	// A device with no serial at all is running ancient firmware (1.0.0.0);
	// fall back to protocol version 0. This is the only way pv0 gets set, and
	// it can only be set automatically (never requested by the caller).
	overrideProtocolVersion := protocolVersion
	if info.SerialNumber == "" {
		overrideProtocolVersion = 0
	}

	// packet_size keys off the *requested* protocol version, like the Rust code.
	packetSize := 512
	if protocolVersion >= 2 {
		packetSize = 1024
	}

	return &Device{
		VID:                        info.VendorID,
		PID:                        info.ProductID,
		SerialNumber:               serial,
		FirmwareVersion:            firmware,
		protocolVersion:            overrideProtocolVersion,
		supportsBothKeypressStates: overrideProtocolVersion > 2,
		supportsBothEncoderStates:  overrideProtocolVersion > 2,
		keyCount:                   keyCount,
		encoderCount:               encoderCount,
		packetSize:                 packetSize,
		conn:                       conn,
		imageCache:                 make(map[byte][]byte),
	}, nil
}

// readFirmwareVersion reads the firmware string via feature report 0x01. On any
// error it returns nil (best-effort). On Windows the Rust crate skips this
// entirely; here it simply fails the read and returns nil, same observable
// result.
func readFirmwareVersion(conn Conn) *string {
	buf := make([]byte, 20)
	buf[0] = 0x01
	n, err := conn.GetFeatureReport(buf)
	if err != nil || n <= 0 {
		return nil
	}
	s := string(buf[:n])
	return &s
}

// WithSupportsBothKeypressStates overrides whether both Up and Down key states
// are handled. Default: protocol_version > 2. Returns the device for chaining.
func (d *Device) WithSupportsBothKeypressStates(supports bool) *Device {
	d.supportsBothKeypressStates = supports
	return d
}

// WithSupportsBothEncoderStates overrides whether both Up and Down encoder
// states are handled. Default: protocol_version > 2.
func (d *Device) WithSupportsBothEncoderStates(supports bool) *Device {
	d.supportsBothEncoderStates = supports
	return d
}

// KeyCount returns the number of keys.
func (d *Device) KeyCount() int { return d.keyCount }

// EncoderCount returns the number of encoders.
func (d *Device) EncoderCount() int { return d.encoderCount }

// ProtocolVersion returns the effective protocol version (may be 0 if the
// device fell back to ancient-firmware mode).
func (d *Device) ProtocolVersion() int { return d.protocolVersion }

// SupportsBothEncoderStates reports the current encoder-state setting.
func (d *Device) SupportsBothEncoderStates() bool { return d.supportsBothEncoderStates }

// Close releases the underlying HID handle.
func (d *Device) Close() error { return d.conn.Close() }

// initialize sends the one-time wake-up handshake (DIS, then LIG). It is
// idempotent: the first call performs the writes, later calls are no-ops.
//
// Port note: like the Rust code, the initialized flag is set *before* the
// writes, so a failed handshake still marks the device initialized.
func (d *Device) initialize() error {
	if d.initialized.Load() {
		return nil
	}
	d.initialized.Store(true)

	// CRT .. DIS
	if err := d.writeExtendedData([]byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x44, 0x49, 0x53}); err != nil {
		return err
	}
	// CRT .. LIG ....
	return d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x4c, 0x49, 0x47, 0x00, 0x00, 0x00, 0x00,
	})
}

// Reset initializes the device, sets brightness to 100% and clears all images.
func (d *Device) Reset() error {
	if err := d.initialize(); err != nil {
		return err
	}
	if err := d.SetBrightness(100); err != nil {
		return err
	}
	return d.ClearAllButtonImages()
}

// SetBrightness sets screen brightness (0-100, clamped).
func (d *Device) SetBrightness(percent uint8) error {
	if err := d.initialize(); err != nil {
		return err
	}
	if percent > 100 {
		percent = 100
	}
	// CRT .. LIG .. <percent>
	return d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x4c, 0x49, 0x47, 0x00, 0x00, percent,
	})
}

// SetLEDBrightness sets the knob-LED brightness (0-100, clamped).
func (d *Device) SetLEDBrightness(percent uint8) error {
	if err := d.initialize(); err != nil {
		return err
	}
	if percent > 100 {
		percent = 100
	}
	// CRT .. LBLIG <percent>
	return d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x4c, 0x42, 0x4c, 0x49, 0x47, percent,
	})
}

// SetLEDColors sets each knob LED's color. colors must contain exactly one
// [r,g,b] entry per LED.
func (d *Device) SetLEDColors(colors [][3]uint8) error {
	if err := d.initialize(); err != nil {
		return err
	}
	// CRT .. SETLB <r,g,b ...>
	buf := []byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x53, 0x45, 0x54, 0x4c, 0x42}
	for _, c := range colors {
		buf = append(buf, c[0], c[1], c[2])
	}
	return d.writeExtendedData(buf)
}

// WriteImage caches raw, already-encoded image bytes for a key. Changes are not
// visible until [Device.Flush]. Mirrors the Rust write_image.
func (d *Device) WriteImage(key uint8, imageData []byte) error {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	cp := make([]byte, len(imageData))
	copy(cp, imageData)
	d.imageCache[key] = cp
	return nil
}

// SetButtonImage encodes img per format and caches it for key. Visible after
// [Device.Flush].
func (d *Device) SetButtonImage(key uint8, format ImageFormat, img Image) error {
	if err := d.initialize(); err != nil {
		return err
	}
	data, err := ConvertImageWithFormat(format, img)
	if err != nil {
		return err
	}
	return d.WriteImage(key, data)
}

// ClearButtonImage blanks a single button (or every button when key == 0xFF).
// Visible after [Device.Flush]; also drops the cached image for that key.
func (d *Device) ClearButtonImage(key uint8) error {
	if err := d.initialize(); err != nil {
		return err
	}
	keyByte := key + 1
	if key == 0xFF {
		keyByte = 0xFF
	}
	// CRT .. CLE .. <keyByte>
	if err := d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4c, 0x45, 0x00, 0x00, 0x00, keyByte,
	}); err != nil {
		return err
	}

	d.cacheMu.Lock()
	delete(d.imageCache, key)
	d.cacheMu.Unlock()
	return nil
}

// ClearAllButtonImages blanks every button. On protocol v2/v3 it also sends the
// STP commit required to actually clear the screen. Empties the image cache.
func (d *Device) ClearAllButtonImages() error {
	if err := d.initialize(); err != nil {
		return err
	}
	if err := d.ClearButtonImage(0xFF); err != nil {
		return err
	}
	if d.protocolVersion >= 2 {
		// CRT .. STP
		if err := d.writeExtendedData([]byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x53, 0x54, 0x50}); err != nil {
			return err
		}
	}

	d.cacheMu.Lock()
	d.imageCache = make(map[byte][]byte)
	d.cacheMu.Unlock()
	return nil
}

// Sleep puts the device to sleep.
func (d *Device) Sleep() error {
	if err := d.initialize(); err != nil {
		return err
	}
	// CRT .. HAN
	return d.writeExtendedData([]byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x48, 0x41, 0x4e})
}

// KeepAlive sends a periodic CONNECT to keep the device awake.
func (d *Device) KeepAlive() error {
	if err := d.initialize(); err != nil {
		return err
	}
	// CRT .. CONNECT
	return d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4f, 0x4e, 0x4e, 0x45, 0x43, 0x54,
	})
}

// Shutdown clears the screen and powers the device down.
func (d *Device) Shutdown() error {
	if err := d.initialize(); err != nil {
		return err
	}
	// CRT .. CLE .. DC
	if err := d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4c, 0x45, 0x00, 0x00, 0x44, 0x43,
	}); err != nil {
		return err
	}
	// CRT .. HAN
	return d.writeExtendedData([]byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x48, 0x41, 0x4e})
}

// SetMode sets the device mode. Some devices (e.g. the MiraBox N1) require the
// correct mode before any other command. The wire value is 0x30 + mode.
func (d *Device) SetMode(mode uint8) error {
	// CRT .. MOD .. <0x30+mode>
	return d.writeExtendedData([]byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x4d, 0x4f, 0x44, 0x00, 0x00, 0x30 + mode,
	})
}

// Flush sends every cached image to the device and commits with STP. The cache
// is drained, so re-flushing without new writes is a no-op.
func (d *Device) Flush() error {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()

	if err := d.initialize(); err != nil {
		return err
	}
	if len(d.imageCache) == 0 {
		return nil
	}

	for key, data := range d.imageCache {
		if err := d.sendImage(key, data); err != nil {
			return err
		}
		delete(d.imageCache, key)
	}

	// CRT .. STP
	return d.writeExtendedData([]byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x53, 0x54, 0x50})
}

// GetReader returns a [DeviceStateReader] that decodes input reports with
// processInput and tracks state to emit press/release/twist events.
func (d *Device) GetReader(processInput ProcessInput) *DeviceStateReader {
	buttons := make([]bool, d.keyCount)
	encoders := make([]bool, d.encoderCount)
	return &DeviceStateReader{
		dev:                        d,
		protocolVersion:            d.protocolVersion,
		supportsBothKeypressStates: d.supportsBothKeypressStates,
		supportsBothEncoderStates:  d.supportsBothEncoderStates,
		processInput:               processInput,
		states:                     DeviceState{Buttons: buttons, Encoders: encoders},
	}
}

// sendImage writes the BAT header announcing image length + target key, then
// streams the image payload in packet-sized chunks. Caller holds cacheMu.
func (d *Device) sendImage(key uint8, imageData []byte) error {
	n := len(imageData)
	// CRT .. BAT .. <len hi> <len lo> <key+1>
	header := []byte{
		0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x42, 0x41, 0x54, 0x00, 0x00,
		byte(n >> 8), byte(n), key + 1,
	}
	if err := d.writeExtendedData(header); err != nil {
		return err
	}
	return d.writeImageDataReports(imageData)
}

// writeImageDataReports splits imageData into report-sized chunks. Each report
// is [0x00 report-id][payload...] zero-padded to packetSize+1. Unlike the Go
// port spec's [0x02,0x01,idx,last] framing, the real protocol (per the Rust
// crate) carries no per-chunk header — length and target were set by sendImage.
func (d *Device) writeImageDataReports(imageData []byte) error {
	reportLength := d.packetSize + 1
	payloadLength := reportLength - 1 // one byte of report-id header

	remaining := len(imageData)
	page := 0
	for remaining > 0 {
		this := remaining
		if this > payloadLength {
			this = payloadLength
		}
		sent := page * payloadLength

		buf := make([]byte, reportLength) // zero-padded by make
		buf[0] = 0x00
		copy(buf[1:], imageData[sent:sent+this])

		if err := d.writeData(buf); err != nil {
			return err
		}

		remaining -= this
		page++
	}
	return nil
}

// writeData writes one output report, serialized against other writers.
func (d *Device) writeData(payload []byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if _, err := d.conn.Write(payload); err != nil {
		return fmt.Errorf("mirajazz: write output report: %w", err)
	}
	return nil
}

// writeExtendedData zero-extends payload to the report size (1 + packetSize) and
// writes it.
func (d *Device) writeExtendedData(payload []byte) error {
	buf := make([]byte, 1+d.packetSize)
	copy(buf, payload)
	return d.writeData(buf)
}

// read performs one input read, serialized against other readers.
func (d *Device) read(buf []byte) (int, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()
	return d.conn.Read(buf)
}

// readWithTimeout performs one input read with a deadline.
func (d *Device) readWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()
	return d.conn.ReadWithTimeout(buf, timeout)
}
