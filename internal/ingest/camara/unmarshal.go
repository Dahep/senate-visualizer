package camara

import "encoding/xml"

// xmlUnmarshal is centralization for the two decode sites (tripwire point:
// a silently-ignoring decoder is exactly the failure mode this contract guards).
func xmlUnmarshal(body []byte, v any) error { return xml.Unmarshal(body, v) }
