module github.com/tokenmp/v3/services/config

go 1.26.5

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.7.4
	github.com/tokenmp/v3/packages/go/httpresp v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/driver/postgres v1.6.0
	gorm.io/gorm v1.31.2
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/tokenmp/v3/packages/go/httpresp => ../../packages/go/httpresp
