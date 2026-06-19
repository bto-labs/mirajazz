package mirajazz

// ProductIDAny (and VendorIDAny) are wildcards for [DeviceQuery]. The Fifine
// Ampligame D6 ships with a product id that varies by batch, so it is matched
// by vendor id + usage page alone (PID left as ProductIDAny).
const (
	VendorIDAny  uint16 = 0
	ProductIDAny uint16 = 0
	UsageAny     uint16 = 0
)

// DeviceQuery selects devices to enumerate or watch. It mirrors the Rust
// DeviceQuery (usage_page, usage_id, vendor_id, product_id), but any field set
// to its *Any zero value acts as a wildcard, which the Rust version lacks. This
// is what lets a single query match a clone family with a varying PID.
type DeviceQuery struct {
	UsagePage uint16
	Usage     uint16
	VendorID  uint16
	ProductID uint16
}

// NewDeviceQuery builds a query. Argument order matches the Rust
// DeviceQuery::new(usage_page, usage_id, vendor_id, product_id).
func NewDeviceQuery(usagePage, usage, vendorID, productID uint16) DeviceQuery {
	return DeviceQuery{
		UsagePage: usagePage,
		Usage:     usage,
		VendorID:  vendorID,
		ProductID: productID,
	}
}

// Matches reports whether info satisfies the query. A query field left at its
// wildcard zero value matches any value.
func (q DeviceQuery) Matches(info DeviceInfo) bool {
	if q.VendorID != VendorIDAny && info.VendorID != q.VendorID {
		return false
	}
	if q.ProductID != ProductIDAny && info.ProductID != q.ProductID {
		return false
	}
	if q.UsagePage != UsageAny && info.UsagePage != q.UsagePage {
		return false
	}
	if q.Usage != UsageAny && info.Usage != q.Usage {
		return false
	}
	return true
}

// MatchesAny reports whether info satisfies any of the queries.
func MatchesAny(queries []DeviceQuery, info DeviceInfo) bool {
	for _, q := range queries {
		if q.Matches(info) {
			return true
		}
	}
	return false
}
