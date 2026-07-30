// Package jsonfile will implement store.Store as one JSON file per
// provider/metering point/day (data/<provider>/<point>/<date>.json),
// written atomically (temp file + rename) so a concurrent reader never
// sees a half-written file when a delayed day gets rewritten.
//
// Not yet implemented.
package jsonfile
