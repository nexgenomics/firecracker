module host_daemon

go 1.25.4

replace ngen/config => ../../go/config

require (
	ngen/config v0.0.0-00010101000000-000000000000
	sdp/datamodel v0.0.0-00010101000000-000000000000
)

require (
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/pquerna/otp v1.5.0 // indirect
)

replace sdp/datamodel => ../../sdp/go/datamodel
