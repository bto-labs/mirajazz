// Package hidapi is the hardware backend for the mirajazz core package. It is
// the only place that links cgo/libhidapi (via github.com/sstallion/go-hid), so
// the protocol logic in the parent package stays testable without hardware.
//
// It supplies the three things the core cannot do itself: enumerating devices,
// opening a handle (as a mirajazz.Conn), and hot-plug watching. async-hid gives
// the Rust crate native hot-plug events; hidapi has none, so [Watch] polls and
// diffs, which is the documented adaptation for this port.
package hidapi

import (
	"fmt"
	"time"

	"github.com/bto-labs/mirajazz-go/internal/mirajazz"
	"github.com/sstallion/go-hid"
)

// Init initializes the global hidapi context. Call once before any other
// function here; pair with [Exit].
func Init() error { return hid.Init() }

// Exit finalizes the global hidapi context.
func Exit() error { return hid.Exit() }

// conn adapts a *hid.Device to the mirajazz.Conn interface.
type conn struct{ dev *hid.Device }

func (c *conn) Write(p []byte) (int, error) { return c.dev.Write(p) }

func (c *conn) Read(p []byte) (int, error) { return c.dev.Read(p) }

func (c *conn) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	return c.dev.ReadWithTimeout(p, timeout)
}

func (c *conn) GetFeatureReport(p []byte) (int, error) { return c.dev.GetFeatureReport(p) }

func (c *conn) Close() error { return c.dev.Close() }

// toInfo converts a go-hid enumeration record into the core's DeviceInfo.
func toInfo(di *hid.DeviceInfo) mirajazz.DeviceInfo {
	return mirajazz.DeviceInfo{
		VendorID:     di.VendorID,
		ProductID:    di.ProductID,
		SerialNumber: di.SerialNbr,
		Path:         di.Path,
		UsagePage:    di.UsagePage,
		Usage:        di.Usage,
	}
}

// List enumerates connected HID interfaces and returns those matching any
// query. Port of mirajazz::device::list_devices.
func List(queries []mirajazz.DeviceQuery) ([]mirajazz.DeviceInfo, error) {
	var out []mirajazz.DeviceInfo
	err := hid.Enumerate(hid.VendorIDAny, hid.ProductIDAny, func(di *hid.DeviceInfo) error {
		info := toInfo(di)
		if mirajazz.MatchesAny(queries, info) {
			out = append(out, info)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hidapi: enumerate: %w", err)
	}
	return out, nil
}

// Open opens the device identified by info (by its HID path) and returns a
// mirajazz.Conn ready to hand to mirajazz.Connect.
func Open(info mirajazz.DeviceInfo) (mirajazz.Conn, error) {
	if info.Path == "" {
		return nil, fmt.Errorf("hidapi: device info has no path")
	}
	dev, err := hid.OpenPath(info.Path)
	if err != nil {
		return nil, fmt.Errorf("hidapi: open %s: %w", info.Path, err)
	}
	return &conn{dev: dev}, nil
}

// OpenFirst enumerates, then opens the first device matching any query. It
// returns the open connection together with the DeviceInfo it matched, which is
// what mirajazz.Connect needs.
func OpenFirst(queries []mirajazz.DeviceQuery) (mirajazz.Conn, mirajazz.DeviceInfo, error) {
	infos, err := List(queries)
	if err != nil {
		return nil, mirajazz.DeviceInfo{}, err
	}
	if len(infos) == 0 {
		return nil, mirajazz.DeviceInfo{}, mirajazz.ErrDeviceNotFound
	}
	c, err := Open(infos[0])
	if err != nil {
		return nil, mirajazz.DeviceInfo{}, err
	}
	return c, infos[0], nil
}
