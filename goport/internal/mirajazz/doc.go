// Package mirajazz is a Go port of the Rust crate 4ndv/mirajazz: a low-level
// library for interfacing with Mirabox and Ajazz "stream controller" devices
// (Stream Deck clones), and clones thereof such as the Fifine Ampligame D6.
//
// The port mirrors the structure of the upstream crate:
//
//   - types.go   -> src/types.rs   (DeviceInput, ImageFormat, ...)
//   - errors.go  -> src/error.rs   (MirajazzError -> typed sentinel errors)
//   - query.go   -> part of src/device.rs (DeviceQuery + matching)
//   - device.go  -> src/device.rs  (Device, connect, the wire protocol)
//   - state.go   -> src/state.rs   (DeviceStateReader, event diffing)
//   - images.go  -> src/images.rs  (image conversion, ImageRect)
//
// Design differences from the Rust crate (all deliberate, all documented at
// their call sites):
//
//   - The async-hid/tokio runtime is replaced by a blocking, synchronous API.
//     There is no async; a reader loop in a single goroutine is the intended
//     usage (see cmd/ampligame-daemon). This matches the Go port spec.
//   - HID access sits behind the small [Conn] interface and the [DeviceInfo]
//     value type. This package contains ZERO hardware/cgo dependencies, so it
//     compiles and unit-tests on any platform without libhidapi. The concrete
//     hidapi-backed implementation lives in the sibling hidapi subpackage.
//   - Image processing uses only the Go standard library (nearest-neighbour
//     resize + image/jpeg + a small BI_RGB BMP encoder) instead of the Rust
//     `image` crate, so there are no third-party dependencies here either.
//
// This package is written to be lifted wholesale into another module (e.g.
// bto-cli's internal/mirajazz). Only the import path needs to change.
package mirajazz
