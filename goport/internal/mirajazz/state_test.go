package mirajazz

import (
	"testing"
	"time"
)

// buttonReport builds a fake 512-byte input report with the ACK prefix, key at
// byte 9 and state at byte 10.
func buttonReport(key, state byte) []byte {
	r := make([]byte, inputReportSize)
	r[0], r[1], r[2] = 65, 67, 75 // ACK
	r[9] = key
	r[10] = state
	return r
}

// fifineProcessInput maps a raw (key,state) to a full 15-button snapshot.
func fifineProcessInput(keyCount int) ProcessInput {
	return func(key, state byte) (DeviceInput, error) {
		buttons := make([]bool, keyCount)
		if int(key) < keyCount {
			buttons[key] = state == 0x01
		}
		return ButtonStateChange(buttons), nil
	}
}

func TestReadEmitsButtonDownThenUp(t *testing.T) {
	c := &fakeConn{reads: [][]byte{
		buttonReport(3, 0x01), // press key 3
		buttonReport(3, 0x00), // release key 3
	}}
	d, _ := Connect(c, testInfo(), 3, 15, 0) // pv3 => both states
	r := d.GetReader(fifineProcessInput(15))

	updates, err := r.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Kind != UpdateButtonDown || updates[0].Index != 3 {
		t.Fatalf("press: got %+v, want one ButtonDown(3)", updates)
	}

	updates, err = r.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Kind != UpdateButtonUp || updates[0].Index != 3 {
		t.Fatalf("release: got %+v, want one ButtonUp(3)", updates)
	}
}

func TestReadNonAckReportIsNoData(t *testing.T) {
	bad := make([]byte, inputReportSize) // no ACK prefix
	bad[9] = 5
	bad[10] = 0x01
	c := &fakeConn{reads: [][]byte{bad}}
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	r := d.GetReader(fifineProcessInput(15))

	updates, err := r.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("non-ACK report should yield no updates, got %+v", updates)
	}
}

func TestReadTimeoutYieldsNoData(t *testing.T) {
	c := &fakeConn{} // no queued reads => ReadWithTimeout returns (0,nil)
	d, _ := Connect(c, testInfo(), 3, 15, 0)
	r := d.GetReader(fifineProcessInput(15))

	to := 10 * time.Millisecond
	updates, err := r.Read(&to)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("timeout should yield no updates, got %+v", updates)
	}
}

func TestSingleStateDeviceSynthesizesDownUp(t *testing.T) {
	// A protocol v1 device does not support both keypress states; a single
	// "pressed" snapshot must synthesize a down+up pair.
	c := &fakeConn{reads: [][]byte{buttonReport(2, 0x01)}}
	info := testInfo()
	d, _ := Connect(c, info, 1, 15, 0) // pv1 => single state, serial hardcoded
	// Force pv1 behaviour even though the empty-serial fallback didn't trigger.
	d.supportsBothKeypressStates = false
	d.protocolVersion = 1
	r := d.GetReader(fifineProcessInput(15))

	updates, err := r.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 ||
		updates[0].Kind != UpdateButtonDown || updates[1].Kind != UpdateButtonUp {
		t.Fatalf("single-state press should synthesize Down+Up, got %+v", updates)
	}
}

func TestEncoderTwistEmitsNonZero(t *testing.T) {
	d, _ := Connect(&fakeConn{}, testInfo(), 3, 15, 2)
	r := d.GetReader(nil)
	updates := r.inputToUpdates(EncoderTwist([]int8{0, -2}))
	if len(updates) != 1 || updates[0].Kind != UpdateEncoderTwist ||
		updates[0].Index != 1 || updates[0].Twist != -2 {
		t.Fatalf("got %+v, want one EncoderTwist(1,-2)", updates)
	}
}
