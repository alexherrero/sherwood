// Package broker supervises the Python broker helper and talks to it over a
// Unix socket, one JSON request and reply per call. The Go application holds no
// broker secrets; only the helper reads them.
//
// It also defines Adapter, the broker interface the execution layer and
// reconciliation use: place, cancel and look up an order, list open orders,
// list holdings, and read cash and account value. The paper adapter is in
// package paper, the scripted fake for tests and the conformance suite every
// adapter passes in package brokertest, and the Robinhood adapter, which
// passes each call to the helper, in package robinhood.
package broker
