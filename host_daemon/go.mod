module host_daemon

go 1.25.4

replace ngen/config => ../../go/config

require (
	github.com/nats-io/nats.go v1.47.0
	github.com/segmentio/kafka-go v0.4.49
	ngen/config v0.0.0-00010101000000-000000000000
	sdp/datamodel v0.0.0-00010101000000-000000000000
)

require (
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	github.com/pquerna/otp v1.5.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
)

replace sdp/datamodel => ../../sdp/go/datamodel
