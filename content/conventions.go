package content

import "strconv"

// Data-model conventions shared by every layer (storage, fields, render,
// build, admin, plugins).
const (
	// BlockTypeKey holds the block name inside each item of a blocks field.
	BlockTypeKey = "_type"
	// ItemKey holds the stable, globally unique ID of a stored group, list
	// item or block ("<table>:<id>"). Items without a valid key are new.
	ItemKey = "_key"
)

// RefID reads a relation value in any of the shapes it takes on its way
// through the system: int64 (store), float64 (JSON), int, string (forms)
// or a resolved reference with a RefID() method (build/templates). It
// reports false for empty, zero, negative and unparsable values.
func RefID(v any) (int64, bool) {
	var id int64
	switch x := v.(type) {
	case int64:
		id = x
	case int:
		id = int64(x)
	case float64:
		id = int64(x)
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, false
		}
		id = n
	case interface{ RefID() int64 }:
		id = x.RefID()
	default:
		return 0, false
	}
	if id <= 0 {
		return 0, false
	}
	return id, true
}
