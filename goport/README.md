# mirajazz-go

A Go port of the [4ndv/mirajazz](https://github.com/4ndv/mirajazz) Rust crate: a
low-level library for Mirabox / Ajazz "stream controller" devices (Stream Deck
clones), targeting the **Fifine Ampligame D6** (15-key, protocol version 3).

This directory is self-contained and builds on its own. It is laid out so the
library packages can be lifted straight into the `bto-cli` Go module — see
[Moving into bto-cli](#moving-into-bto-cli).

## Layout

```
goport/
├── go.mod                          module github.com/bto-labs/mirajazz-go
├── internal/
│   └── mirajazz/                   ← the port. stdlib only, zero hardware deps.
│       ├── doc.go                  package overview + map to the Rust sources
│       ├── types.go                Conn, DeviceInfo, DeviceInput, ImageFormat…
│       ├── errors.go               sentinel errors (← MirajazzError enum)
│       ├── query.go                DeviceQuery + wildcard matching
│       ├── device.go               Device + the wire protocol (← device.rs)
│       ├── state.go                DeviceStateReader, event diffing (← state.rs)
│       ├── images.go               image convert + BMP/JPEG encode (← images.rs)
│       └── *_test.go               unit tests (run without hardware)
│       └── hidapi/                 ← the ONLY cgo/libhidapi code.
│           ├── hidapi.go           enumerate / open (go-hid → mirajazz.Conn)
│           └── watcher.go          polling hot-plug watcher
├── cmd/
│   └── ampligame-daemon/main.go    reference daemon (button → webhook)
└── udev/40-fifine-streamdeck.rules Linux permissions for non-root access
```

The split is the key design decision: **`internal/mirajazz` has no hardware
dependency**, so all protocol/state/image logic compiles and unit-tests on any
platform without `libhidapi`. Only `internal/mirajazz/hidapi` links cgo.

## Build & test

```sh
# Pure logic — no system libraries needed:
go test ./internal/mirajazz/
go vet  ./internal/mirajazz/

# Full build incl. the cgo HID backend + daemon needs hidapi headers:
sudo apt-get install -y libhidapi-dev libudev-dev libusb-1.0-0-dev build-essential
go build ./...
go build -o ampligame-daemon ./cmd/ampligame-daemon
```

## Running the daemon

1. Install the udev rules and make sure you are in `plugdev`:
   ```sh
   sudo cp udev/40-fifine-streamdeck.rules /etc/udev/rules.d/
   sudo udevadm control --reload-rules && sudo udevadm trigger
   groups | grep plugdev   # else: sudo usermod -aG plugdev "$USER" and re-login
   ```
2. Plug in the D6 and run `./ampligame-daemon`. Press keys; each press logs a
   `[Hardware Event]` line and POSTs a webhook to the configured k3s ingress.

## Protocol notes / divergences from the spec

This port follows the **Rust crate** as the protocol authority. Two things
differ from the "Go Port requirements" draft, deliberately:

- **Button reads.** The crate does *not* parse `buf[1]`/`buf[2]`. A valid input
  report is prefixed with `ACK` (`0x41 0x43 0x4B`); the changed key index is at
  **byte 9** and its state at **byte 10** (protocol v3 reports both press and
  release). `state.go` implements this; the draft's `buf[1]/buf[2]` reading was
  a simplification and would mis-decode real hardware.
- **Image chunking.** The crate frames an image with a single `BAT` header
  (length + target key) followed by report-sized chunks that carry only the
  `0x00` report-id byte — there is **no** per-chunk `[0x02,0x01,idx,last]`
  header. `device.go`'s `writeImageDataReports` matches the crate.

Other intentional adaptations (all noted at their call sites):

- Async (tokio/async-hid) → synchronous blocking API + a goroutine reader loop.
- `Connect` returns `ErrInvalidProtocolVersion` instead of `assert!`-panicking.
- The firmware read is best-effort (nil on failure) rather than fatal.
- Hot-plug watching polls + diffs (hidapi has no native hot-plug events).
- Images use only the Go stdlib (nearest-neighbour resize + `image/jpeg` + a
  small BI_RGB BMP encoder) instead of the Rust `image` crate.

## Protocol-version cheat sheet (from the crate)

| pv | packet | both key states | serial | notes |
|----|--------|-----------------|--------|-------|
| 0  | 512    | no              | none   | auto-fallback for 1.0.0.0 firmware |
| 1  | 512    | no              | hardcoded `355499441494` | AKP153 etc. |
| 2  | 1024   | no              | unique | needs STP to clear screen |
| 3  | 1024   | **yes**         | unique | gifs; **the Ampligame D6** |

## Moving into bto-cli

The library is written to drop into `bto-cli` with **only an import-path change**:

1. Copy `internal/mirajazz/` (including `internal/mirajazz/hidapi/`) into
   `bto-cli/internal/mirajazz/`.
2. Rewrite the import path in every file:
   ```sh
   grep -rl 'github.com/bto-labs/mirajazz-go/internal/mirajazz' . \
     | xargs sed -i 's#github.com/bto-labs/mirajazz-go/internal/mirajazz#<bto-cli-module>/internal/mirajazz#g'
   ```
   (Replace `<bto-cli-module>` with bto-cli's module path.)
3. Add `github.com/sstallion/go-hid` to bto-cli's `go.mod` (`go mod tidy`).
4. The reference daemon in `cmd/ampligame-daemon` shows the wiring; in bto-cli
   this becomes a subcommand. Per bto-cli conventions, the HID **read loop is a
   detection-style concern** (sudo-free; relies on the udev rule), while any
   device writes that touch the screen would be a manage-style action.

Because the core package is hardware-free, bto-cli's existing unit-test setup
can exercise it with the in-memory `Conn` fake (see `device_test.go`) with no
cgo and no attached device.
