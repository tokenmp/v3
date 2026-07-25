module github.com/tokenmp/v3/services/api

go 1.26.5

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/oapi-codegen/runtime v1.6.0
	github.com/tokenmp/v3/packages/go/httpresp v0.0.0-00010101000000-000000000000
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/google/uuid v1.6.0
)

replace github.com/tokenmp/v3/packages/go/httpresp => ../../packages/go/httpresp
