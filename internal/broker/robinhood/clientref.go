package robinhood

import (
	"crypto/sha1" //nolint:gosec // G505: UUIDv5 is defined over SHA-1; this is a name, not a secret
	"encoding/hex"
)

// clientRefNamespace is the namespace of every client reference, written out
// as bytes. It is uuid5(NAMESPACE_URL,
// "https://github.com/alexherrero/sherwood/broker/client-ref"), the same
// fixed value the helper uses (b2c9bd9c-91ea-56fa-8a48-f93a5fccfc5f).
// Changing it would make every order already sent unfindable.
var clientRefNamespace = [16]byte{
	0xb2, 0xc9, 0xbd, 0x9c, 0x91, 0xea, 0x56, 0xfa,
	0x8a, 0x48, 0xf9, 0x3a, 0x5f, 0xcc, 0xfc, 0x5f,
}

// ClientRef is the client reference the helper sends to the broker for the
// order with idempotency key key: a version 5 UUID of the key under a fixed
// namespace, written in lower case. It is one-way, so the adapter maps an
// open order back to its key by computing ClientRef for each key sherwood
// holds.
func ClientRef(key string) string { return uuid5(clientRefNamespace, key) }

// uuid5 is RFC 4122's name-based UUID: the first 16 bytes of the SHA-1 of the
// namespace's bytes followed by the name's, with the version nibble set to 5
// and the variant bits to 10.
func uuid5(namespace [16]byte, name string) string {
	h := sha1.New() //nolint:gosec // G401: see the import
	h.Write(namespace[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}
