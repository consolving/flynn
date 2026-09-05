module github.com/flynn/flynn/pkg/autocert/pebbletest

go 1.25.0

require (
	github.com/jmhodges/clock v1.2.0
	github.com/letsencrypt/pebble v1.0.1
)

require (
	golang.org/x/crypto v0.0.0-20181203042331-505ab145d0a9 // indirect
	gopkg.in/square/go-jose.v2 v2.1.9 // indirect
)

replace github.com/jmhodges/clock => github.com/jmhodges/clock v1.2.0
