"""Sherwood's broker helper.

The helper is the only process that talks to the broker and the only one that
reads the broker secrets. The Go application starts it, watches it and sends it
one JSON request per connection over a Unix socket; the helper answers with one
JSON reply and never retries anything.
"""

__version__ = "0.1.0"
