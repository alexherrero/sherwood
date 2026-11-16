// Package broker supervises the Python broker helper and talks to it over a
// Unix socket, one JSON request and reply per call. The Go application holds no
// broker secrets; only the helper reads them.
package broker
