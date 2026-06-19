package mirajazz

import (
	"errors"
	"testing"
	"time"
)

// fakeConn is an in-memory Conn for exercising the protocol without hardware.
type fakeConn struct {
	writes   [][]byte
	reads    [][]byte // queued input reports returned by Read in order
	feature  []byte   // bytes returned by GetFeatureReport (after the report id)
	readErr  error
	writeErr error
	closed   bool
}

func (c *fakeConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	c.writes = append(c.writes, cp)
	return len(p), nil
}

func (c *fakeConn) Read(p []byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	if len(c.reads) == 0 {
		return 0, errors.New("no more reads")
	}
	r := c.reads[0]
	c.reads = c.reads[1:]
	n := copy(p, r)
	return n, nil
}

func (c *fakeConn) ReadWithTimeout(p []byte, _ time.Duration) (int, error) {
	if len(c.reads) == 0 {
		return 0, nil // simulate timeout
	}
	return c.Read(p)
}

func (c *fakeConn) GetFeatureReport(p []byte) (int, error) {
	if c.feature == nil {
		return 0, errors.New("no feature report")
	}
	n := copy(p, c.feature)
	return n, nil
}

func (c *fakeConn) Close() error { c.closed = true; return nil }

func testInfo() DeviceInfo {
	return DeviceInfo{VendorID: 0x3142, ProductID: 0x1003, SerialNumber: "ABC123"}
}

func TestConnectProtocolVersionValidation(t *testing.T) {
	for _, pv := range []int{0, 4, -1} {
		if _, err := Connect(&fakeConn{}, testInfo(), pv, 15, 0); !errors.Is(err, ErrInvalidProtocolVersion) {
			t.Fatalf("pv=%d: want ErrInvalidProtocolVersion, got %v", pv, err)
		}
	}
}

func TestConnectV3PacketSizeAndFlags(t *testing.T) {
	d, err := Connect(&fakeConn{}, testInfo(), 3, 15, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.packetSize != 1024 {
		t.Errorf("packetSize = %d, want 1024", d.packetSize)
	}
	if !d.supportsBothKeypressStates {
		t.Error("pv3 should support both keypress states")
	}
	if d.SerialNumber != "ABC123" {
		t.Errorf("serial = %q, want ABC123", d.SerialNumber)
	}
}

func TestConnectV1HardcodesSerialAndPacketSize(t *testing.T) {
	info := testInfo()
	info.SerialNumber = "" // v1 devices often report no serial
	d, err := Connect(&fakeConn{}, info, 1, 18, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.SerialNumber != "355499441494" {
		t.Errorf("serial = %q, want hardcoded v1 serial", d.SerialNumber)
	}
	if d.packetSize != 512 {
		t.Errorf("packetSize = %d, want 512", d.packetSize)
	}
	// Empty raw serial forces a protocol-version-0 fallback.
	if d.protocolVersion != 0 {
		t.Errorf("protocolVersion = %d, want 0 fallback", d.protocolVersion)
	}
}

func TestConnectV2MissingSerialIsInvalid(t *testing.T) {
	info := testInfo()
	info.SerialNumber = ""
	if _, err := Connect(&fakeConn{}, info, 2, 6, 0); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("want ErrInvalidDevice, got %v", err)
	}
}

func TestFirmwareVersionBestEffort(t *testing.T) {
	c := &fakeConn{feature: []byte{0x01, '1', '.', '0', '.', '5'}}
	d, err := Connect(c, testInfo(), 3, 15, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.FirmwareVersion == nil {
		t.Fatal("expected firmware version")
	}
	// No feature report -> nil, but connect still succeeds.
	d2, err := Connect(&fakeConn{}, testInfo(), 3, 15, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d2.FirmwareVersion != nil {
		t.Errorf("want nil firmware, got %q", *d2.FirmwareVersion)
	}
}

func TestExtendedDataIsPaddedToReportSize(t *testing.T) {
	c := &fakeConn{}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	if err := d.SetBrightness(50); err != nil {
		t.Fatal(err)
	}
	// initialize() sends 2 reports, then SetBrightness sends 1 => 3 writes.
	if len(c.writes) != 3 {
		t.Fatalf("got %d writes, want 3", len(c.writes))
	}
	for i, w := range c.writes {
		if len(w) != 1025 {
			t.Errorf("write %d len = %d, want 1025 (1 + 1024)", i, len(w))
		}
	}
	last := c.writes[2]
	if last[11] != 50 {
		t.Errorf("brightness byte = %d, want 50", last[11])
	}
}

func TestBrightnessClamped(t *testing.T) {
	c := &fakeConn{}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	if err := d.SetBrightness(200); err != nil {
		t.Fatal(err)
	}
	if got := c.writes[len(c.writes)-1][11]; got != 100 {
		t.Errorf("clamped brightness = %d, want 100", got)
	}
}

func TestInitializeIsIdempotent(t *testing.T) {
	c := &fakeConn{}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	_ = d.initialize()
	_ = d.initialize()
	if len(c.writes) != 2 {
		t.Errorf("initialize wrote %d reports across two calls, want 2", len(c.writes))
	}
}

func TestFlushSendsImagesThenSTP(t *testing.T) {
	c := &fakeConn{}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	_ = d.initialize() // get the one-time handshake out of the way
	// 2100 bytes => BAT header + 3 image chunks (1024,1024,52) for one key.
	img := make([]byte, 2100)
	if err := d.WriteImage(4, img); err != nil {
		t.Fatal(err)
	}
	c.writes = nil // count only the flush writes
	if err := d.Flush(); err != nil {
		t.Fatal(err)
	}
	// header(1) + chunks(3) + STP(1) = 5
	if len(c.writes) != 5 {
		t.Fatalf("flush wrote %d reports, want 5", len(c.writes))
	}
	header := c.writes[0]
	// BAT header carries length (2100 = 0x0834) and key+1 (=5).
	if header[11] != 0x08 || header[12] != 0x34 {
		t.Errorf("length bytes = %#x %#x, want 0x08 0x34", header[11], header[12])
	}
	if header[13] != 5 {
		t.Errorf("key byte = %d, want 5 (key 4 + 1)", header[13])
	}
	stp := c.writes[4]
	// STP = 0x53 0x54 0x50 at offset 6..9
	if stp[6] != 0x53 || stp[7] != 0x54 || stp[8] != 0x50 {
		t.Errorf("final report is not STP: % x", stp[6:9])
	}
	// Cache drained: a second flush is a no-op.
	c.writes = nil
	if err := d.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 0 {
		t.Errorf("second flush wrote %d reports, want 0", len(c.writes))
	}
}

func TestClearButtonImageKeyByte(t *testing.T) {
	c := &fakeConn{}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	_ = d.initialize()
	c.writes = nil
	if err := d.ClearButtonImage(7); err != nil {
		t.Fatal(err)
	}
	if got := c.writes[0][12]; got != 8 {
		t.Errorf("clear key byte = %d, want 8 (key 7 + 1)", got)
	}
	c.writes = nil
	if err := d.ClearButtonImage(0xFF); err != nil {
		t.Fatal(err)
	}
	if got := c.writes[0][12]; got != 0xFF {
		t.Errorf("clear-all key byte = %#x, want 0xFF", got)
	}
}

func TestWriteErrorPropagates(t *testing.T) {
	c := &fakeConn{writeErr: errors.New("boom")}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	if err := d.SetBrightness(50); err == nil {
		t.Fatal("expected write error to propagate")
	}
}
