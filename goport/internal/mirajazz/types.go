package mirajazz

import "time"

// Conn is the minimal synchronous HID read/write surface a [Device] needs.
//
// It is deliberately tiny so the protocol logic in this package stays free of
// any hardware dependency: production code passes a hidapi-backed Conn (see the
// hidapi subpackage), tests pass an in-memory fake. All buffers include the HID
// report id as their first byte, exactly like hidapi.
type Conn interface {
	// Write sends an output report. p[0] is the report id.
	Write(p []byte) (int, error)
	// Read blocks until an input report is available, copying it into p.
	Read(p []byte) (int, error)
	// ReadWithTimeout behaves like Read but returns (0, nil) if no report
	// arrives within timeout.
	ReadWithTimeout(p []byte, timeout time.Duration) (int, error)
	// GetFeatureReport reads a feature report; p[0] is the report id on input.
	GetFeatureReport(p []byte) (int, error)
	// Close releases the device handle.
	Close() error
}

// DeviceInfo is an enumerated HID device, the port's stand-in for async-hid's
// DeviceInfo. It is a plain value so it can be compared and used as a map key.
type DeviceInfo struct {
	VendorID     uint16
	ProductID    uint16
	SerialNumber string
	// Path is the OS-specific HID path used to (re)open the device.
	Path string
	// UsagePage and Usage identify the specific HID interface; these devices
	// expose several, and only one carries the stream-controller protocol.
	UsagePage uint16
	Usage     uint16
}

// LifecycleEventKind distinguishes connect from disconnect.
type LifecycleEventKind int

const (
	// DeviceConnected is emitted when a matching device appears.
	DeviceConnected LifecycleEventKind = iota
	// DeviceDisconnected is emitted when a previously seen device goes away.
	DeviceDisconnected
)

// LifecycleEvent is a connect/disconnect notification from a watcher, the
// port's stand-in for the Rust DeviceLifecycleEvent enum.
type LifecycleEvent struct {
	Kind LifecycleEventKind
	Info DeviceInfo
}

// InputKind tags a [DeviceInput], replacing the Rust DeviceInput enum.
type InputKind int

const (
	// InputNoData means the device produced nothing this read.
	InputNoData InputKind = iota
	// InputButtonStateChange carries a full snapshot of button states.
	InputButtonStateChange
	// InputEncoderStateChange carries a full snapshot of encoder press states.
	InputEncoderStateChange
	// InputEncoderTwist carries per-encoder rotation deltas.
	InputEncoderTwist
)

// DeviceInput is the normalized result of decoding one input report. Exactly
// one of the slices is meaningful, selected by Kind. It is produced by a
// [ProcessInput] function supplied per device.
type DeviceInput struct {
	Kind InputKind
	// Buttons is the full button-state snapshot for InputButtonStateChange.
	Buttons []bool
	// Encoders is the full encoder press snapshot for InputEncoderStateChange.
	Encoders []bool
	// Twist holds per-encoder signed rotation deltas for InputEncoderTwist.
	Twist []int8
}

// NoData returns an empty DeviceInput.
func NoData() DeviceInput { return DeviceInput{Kind: InputNoData} }

// ButtonStateChange returns a button-snapshot DeviceInput.
func ButtonStateChange(buttons []bool) DeviceInput {
	return DeviceInput{Kind: InputButtonStateChange, Buttons: buttons}
}

// EncoderStateChange returns an encoder press-snapshot DeviceInput.
func EncoderStateChange(encoders []bool) DeviceInput {
	return DeviceInput{Kind: InputEncoderStateChange, Encoders: encoders}
}

// EncoderTwist returns an encoder rotation DeviceInput.
func EncoderTwist(twist []int8) DeviceInput {
	return DeviceInput{Kind: InputEncoderTwist, Twist: twist}
}

// IsEmpty reports whether no data was received.
func (d DeviceInput) IsEmpty() bool { return d.Kind == InputNoData }

// ProcessInput maps the raw (key, state) bytes of an input report to a
// normalized [DeviceInput]. It is supplied per device because the raw byte ->
// button/encoder layout is device-specific (see the akp153 example in the Rust
// crate, which offsets indices). key is report byte 9; state is report byte 10
// (or a synthetic 0x01 for devices without both-keypress-state support).
type ProcessInput func(key, state byte) (DeviceInput, error)

// ImageMode is the on-wire image encoding.
type ImageMode int

const (
	// ImageModeNone produces no image bytes.
	ImageModeNone ImageMode = iota
	// ImageModeBMP encodes a 24-bit BI_RGB bitmap.
	ImageModeBMP
	// ImageModeJPEG encodes a quality-90 JPEG (used by protocol v2/v3).
	ImageModeJPEG
)

// ImageRotation is applied after resizing.
type ImageRotation int

const (
	// Rot0 applies no rotation.
	Rot0 ImageRotation = iota
	// Rot90 rotates 90 degrees clockwise.
	Rot90
	// Rot180 rotates 180 degrees.
	Rot180
	// Rot270 rotates 90 degrees counter-clockwise.
	Rot270
)

// ImageMirroring is applied after rotation.
type ImageMirroring int

const (
	// MirrorNone applies no mirroring.
	MirrorNone ImageMirroring = iota
	// MirrorX flips horizontally.
	MirrorX
	// MirrorY flips vertically.
	MirrorY
	// MirrorBoth flips on both axes.
	MirrorBoth
)

// ImageFormat describes how an image is prepared for a specific device/button.
type ImageFormat struct {
	Mode     ImageMode
	Width    int
	Height   int
	Rotation ImageRotation
	Mirror   ImageMirroring
}
