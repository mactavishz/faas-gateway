package handlers

import (
	"net/http"
	"strconv"
	"time"
)

// ArrivalHeader carries the gateway arrival time of a function invocation
// (host-clock milliseconds since the Unix epoch).
const ArrivalHeader = "X-Faas-Arrival"

// MakeArrivalStampMiddleware records the gateway arrival time of each function
// invocation before any scale-from-zero happens.
func MakeArrivalStampMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(ArrivalHeader, strconv.FormatInt(time.Now().UnixMilli(), 10))
		next(w, r)
	}
}
