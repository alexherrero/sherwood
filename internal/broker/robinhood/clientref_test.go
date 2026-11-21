package robinhood

import (
	"strings"
	"testing"
)

// The helper's test vector, pinned on both sides of the socket
// (brokerhelper/tests/test_order_ops.py pins the same one).
func TestClientRefMatchesTheHelpersVector(t *testing.T) {
	if got, want := ClientRef(strings.Repeat("a", 64)), "bfc5ea18-c366-55c9-8118-5fae949d99bc"; got != want {
		t.Fatalf("ClientRef(a*64) = %s, want %s", got, want)
	}
}

// The namespace is itself a UUIDv5 of a fixed URL under RFC 4122's URL
// namespace; working it out here pins the bytes written in clientref.go.
func TestClientRefNamespaceIsDerivedFromItsURL(t *testing.T) {
	url := [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	got := uuid5(url, "https://github.com/alexherrero/sherwood/broker/client-ref")
	if want := "b2c9bd9c-91ea-56fa-8a48-f93a5fccfc5f"; got != want {
		t.Fatalf("namespace = %s, want %s", got, want)
	}
	if got != formatUUID(clientRefNamespace) {
		t.Fatalf("clientRefNamespace bytes %x do not spell %s", clientRefNamespace, got)
	}
	// RFC 4122's own example: the DNS namespace's UUIDv5 of "python.org".
	dns := [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	if got, want := uuid5(dns, "python.org"), "886313e1-3b8a-5372-9b90-0c9aee199e5d"; got != want {
		t.Fatalf("uuid5(DNS, python.org) = %s, want %s", got, want)
	}
}

func TestClientRefIsAVersion5UUIDAndDiffersPerKey(t *testing.T) {
	seen := map[string]string{}
	for _, key := range []string{"", "k", strings.Repeat("1", 64), strings.Repeat("2", 64), "ünïcode"} {
		ref := ClientRef(key)
		if len(ref) != 36 || ref[14] != '5' || !strings.ContainsRune("89ab", rune(ref[19])) || strings.ToLower(ref) != ref {
			t.Errorf("ClientRef(%q) = %s, not a lower-case version 5 UUID", key, ref)
		}
		if other, ok := seen[ref]; ok {
			t.Errorf("keys %q and %q share reference %s", key, other, ref)
		}
		seen[ref] = key
		if ClientRef(key) != ref {
			t.Errorf("ClientRef(%q) is not deterministic", key)
		}
	}
}

func formatUUID(u [16]byte) string {
	const hexdigits = "0123456789abcdef"
	var b strings.Builder
	for i, c := range u {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			b.WriteByte('-')
		}
		b.WriteByte(hexdigits[c>>4])
		b.WriteByte(hexdigits[c&0x0f])
	}
	return b.String()
}
