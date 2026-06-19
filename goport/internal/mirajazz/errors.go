package mirajazz

import "errors"

// Sentinel errors mirroring the variants of the Rust `MirajazzError` enum.
// Underlying HID and image-encoding failures are wrapped with %w rather than
// being given their own variants, so callers can use errors.Is / errors.As.
var (
	// ErrWatcherAlreadyInitialized is returned when a watcher is started twice.
	ErrWatcherAlreadyInitialized = errors.New("mirajazz: watcher already initialized")

	// ErrDeviceNotFound is returned when no device matches the provided info.
	ErrDeviceNotFound = errors.New("mirajazz: device not found")

	// ErrInvalidDevice is returned when a device is missing required metadata
	// (for example a protocol-version >= 2 device with no serial number).
	ErrInvalidDevice = errors.New("mirajazz: invalid device")

	// ErrNoScreen is returned when there is nowhere to write an image.
	ErrNoScreen = errors.New("mirajazz: no screen")

	// ErrInvalidKeyIndex is returned for an out-of-range key index.
	ErrInvalidKeyIndex = errors.New("mirajazz: invalid key index")

	// ErrUnrecognizedPID is returned for an unrecognized product id.
	ErrUnrecognizedPID = errors.New("mirajazz: unrecognized product id")

	// ErrUnsupportedOperation is returned when a device cannot perform an op.
	ErrUnsupportedOperation = errors.New("mirajazz: unsupported operation")

	// ErrBadData is returned when the device sends unexpected data.
	ErrBadData = errors.New("mirajazz: bad data from device")

	// ErrInvalidProtocolVersion is returned by Connect for a protocol version
	// outside the supported 1..=3 range. In the Rust crate this is an assert!;
	// here it is a recoverable error so a library caller cannot panic a host.
	ErrInvalidProtocolVersion = errors.New("mirajazz: invalid protocol version (must be 1..=3)")
)
