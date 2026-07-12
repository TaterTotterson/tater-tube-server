package utils

// contextKey is a type for context keys to avoid collisions
type contextKey string

func (c contextKey) String() string {
	return "server context key " + string(c)
}

// Context keys for passing Server request metadata through context
const (
	ContentLengthKey          = contextKey("contentLength")
	RangeKey                  = contextKey("rangeKey")
	IsCopy                    = contextKey("isCopy")
	Origin                    = contextKey("origin")
	ShowCorrupted             = contextKey("showCorrupted")
	ActiveStreamKey           = contextKey("activeStream")
	StreamIDKey               = contextKey("streamID")
	StreamSourceKey           = contextKey("streamSource")
	StreamPlayerIDKey         = contextKey("streamPlayerID")
	StreamUserNameKey         = contextKey("streamUserName")
	ClientIPKey               = contextKey("clientIP")
	UserAgentKey              = contextKey("userAgent")
	MaxPrefetchKey            = contextKey("maxPrefetch")
	SuppressStreamTrackingKey = contextKey("suppressStreamTracking")
)
