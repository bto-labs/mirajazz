package mirajazz

import (
	"bytes"
	"sync"
	"time"
)

// inputReportSize is the number of bytes read per input report. The Rust crate
// reads 512 here for every protocol version (the key/state bytes live well
// inside the first 512 bytes even on 1024-byte devices), so the port matches.
const inputReportSize = 512

// ackPrefix marks a valid input report ("ACK"). Protocol version 0 devices do
// not emit it; for every other version a report lacking it is ignored.
var ackPrefix = []byte{65, 67, 75}

// UpdateKind tags a [DeviceStateUpdate].
type UpdateKind int

const (
	// UpdateButtonDown - a button was pressed.
	UpdateButtonDown UpdateKind = iota
	// UpdateButtonUp - a button was released.
	UpdateButtonUp
	// UpdateEncoderDown - an encoder was pressed.
	UpdateEncoderDown
	// UpdateEncoderUp - an encoder was released.
	UpdateEncoderUp
	// UpdateEncoderTwist - an encoder was turned (see Twist for the delta).
	UpdateEncoderTwist
)

// DeviceStateUpdate is a single high-level event derived by diffing successive
// input snapshots. It replaces the Rust DeviceStateUpdate enum.
type DeviceStateUpdate struct {
	Kind  UpdateKind
	Index uint8
	// Twist is the signed rotation delta, valid only for UpdateEncoderTwist.
	Twist int8
}

// DeviceState is the last-seen snapshot of button and encoder press states.
type DeviceState struct {
	// Buttons includes touch-point state.
	Buttons  []bool
	Encoders []bool
}

// DeviceStateReader reads input reports and turns them into discrete
// [DeviceStateUpdate] events. Only one reader should be active per device at a
// time. It is the port of the Rust DeviceStateReader.
type DeviceStateReader struct {
	dev *Device

	protocolVersion            int
	supportsBothKeypressStates bool
	supportsBothEncoderStates  bool
	processInput               ProcessInput

	mu     sync.Mutex
	states DeviceState
}

// RawReadData reads exactly length bytes of an input report (blocking).
func (r *DeviceStateReader) RawReadData(length int) ([]byte, error) {
	buf := make([]byte, length)
	if _, err := r.dev.read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// RawReadDataWithTimeout reads up to length bytes, returning (nil, nil) if the
// timeout elapses before any report arrives.
func (r *DeviceStateReader) RawReadDataWithTimeout(length int, timeout time.Duration) ([]byte, error) {
	buf := make([]byte, length)
	n, err := r.dev.readWithTimeout(buf, timeout)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return buf, nil
}

// ReadInput reads one report and decodes it with processInput. timeout nil means
// block indefinitely. A timed-out or non-ACK report decodes to NoData.
func (r *DeviceStateReader) ReadInput(timeout *time.Duration, processInput ProcessInput) (DeviceInput, error) {
	var data []byte
	var err error
	if timeout != nil {
		data, err = r.RawReadDataWithTimeout(inputReportSize, *timeout)
	} else {
		data, err = r.RawReadData(inputReportSize)
	}
	if err != nil {
		return NoData(), err
	}
	if data == nil {
		return NoData(), nil
	}

	// Skip the ACK check for protocol version 0 (very old firmware does not
	// prefix packets with ACK).
	if r.protocolVersion > 0 && !bytes.HasPrefix(data, ackPrefix) {
		return NoData(), nil
	}

	// Guard against short reports before indexing bytes 9 and 10.
	if len(data) <= 10 {
		return NoData(), nil
	}

	state := byte(0x01)
	if r.supportsBothKeypressStates {
		state = data[10]
	}
	return processInput(data[9], state)
}

// Read reads one report and returns the resulting high-level updates. timeout
// nil blocks indefinitely.
func (r *DeviceStateReader) Read(timeout *time.Duration) ([]DeviceStateUpdate, error) {
	input, err := r.ReadInput(timeout, r.processInput)
	if err != nil {
		return nil, err
	}
	return r.inputToUpdates(input), nil
}

// inputToUpdates diffs a decoded input against the stored state and returns the
// events. It also advances the stored state. Mirrors the Rust input_to_updates.
func (r *DeviceStateReader) inputToUpdates(input DeviceInput) []DeviceStateUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()

	var updates []DeviceStateUpdate

	switch input.Kind {
	case InputButtonStateChange:
		n := min(len(input.Buttons), len(r.states.Buttons))
		for i := 0; i < n; i++ {
			their := input.Buttons[i]
			mine := r.states.Buttons[i]
			if !r.supportsBothKeypressStates {
				// Devices without both states report only presses; synthesize
				// an immediate down+up pair.
				if their {
					updates = append(updates,
						DeviceStateUpdate{Kind: UpdateButtonDown, Index: uint8(i)},
						DeviceStateUpdate{Kind: UpdateButtonUp, Index: uint8(i)},
					)
				}
			} else if their != mine {
				if their {
					updates = append(updates, DeviceStateUpdate{Kind: UpdateButtonDown, Index: uint8(i)})
				} else {
					updates = append(updates, DeviceStateUpdate{Kind: UpdateButtonUp, Index: uint8(i)})
				}
			}
		}
		r.states.Buttons = input.Buttons

	case InputEncoderStateChange:
		n := min(len(input.Encoders), len(r.states.Encoders))
		for i := 0; i < n; i++ {
			their := input.Encoders[i]
			mine := r.states.Encoders[i]
			if !r.supportsBothEncoderStates {
				if their {
					updates = append(updates,
						DeviceStateUpdate{Kind: UpdateEncoderDown, Index: uint8(i)},
						DeviceStateUpdate{Kind: UpdateEncoderUp, Index: uint8(i)},
					)
				}
			} else if their != mine {
				if their {
					updates = append(updates, DeviceStateUpdate{Kind: UpdateEncoderDown, Index: uint8(i)})
				} else {
					updates = append(updates, DeviceStateUpdate{Kind: UpdateEncoderUp, Index: uint8(i)})
				}
			}
		}
		r.states.Encoders = input.Encoders

	case InputEncoderTwist:
		for i, change := range input.Twist {
			if change != 0 {
				updates = append(updates, DeviceStateUpdate{Kind: UpdateEncoderTwist, Index: uint8(i), Twist: change})
			}
		}
	}

	return updates
}
