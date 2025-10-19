package protocol

const TYPE_MESSAGE uint8 = 0
const TYPE_ERROR uint8 = 1
const TYPE_STATUS uint8 = 2
const TYPE_KEY_EXCHANGE uint8 = 3
const TYPE_DEBUG uint8 = 4
const TYPE_MULTIPART uint8 = 5
const TYPE_CLEARTEXT uint8 = 6

// Bit flags
const TYPE_COMPRESSED_GZIP uint8 = 32
const TYPE_COMPRESSED_ZSTD uint8 = 64

const IPV4_HEADER_SIZE = 20
const IPV6_HEADER_SIZE = 40
const UDP_HEADER_SIZE = 8
const TCP_HEADER_SIZE = 20
const HEADER_SIZE = IPV6_HEADER_SIZE + UDP_HEADER_SIZE

// Return if messageType matches messageType2
// messageType is a compound of bit flags (32 and above) and
// message types below 32
func IsMessageType(messageType uint8, messageType2 uint8) bool {
	if messageType >= TYPE_COMPRESSED_GZIP {
		return (messageType & messageType2) == messageType2
	}
	// Not bit flags
	return messageType == messageType2
}

// Can add a flag to a message type
func Merge(messageType uint8, messageType2 uint8) uint8 {
	return messageType | messageType2
}
