module github.com/cloud-barista/cb-tumblebug

go 1.16

replace (
	github.com/coreos/bbolt => go.etcd.io/bbolt v1.3.3
	github.com/coreos/go-systemd => github.com/coreos/go-systemd/v22 v22.0.0
	google.golang.org/grpc => google.golang.org/grpc v1.26.0
)

require (
	github.com/bramvdbogaerde/go-scp v1.0.0
	github.com/cloud-barista/cb-dragonfly v0.4.3
	github.com/cloud-barista/cb-log v0.4.0
	github.com/cloud-barista/cb-spider v0.4.18
	github.com/cloud-barista/cb-store v0.4.1
	github.com/go-playground/validator/v10 v10.9.0
	github.com/go-resty/resty/v2 v2.7.0
	github.com/go-sql-driver/mysql v1.6.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang-jwt/jwt/v4 v4.1.0
	github.com/golang/protobuf v1.5.2
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/labstack/echo/v4 v4.6.1
	github.com/mattn/go-sqlite3 v2.0.3+incompatible
	github.com/opentracing/opentracing-go v1.2.0
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/common v0.29.0 // indirect
	github.com/rs/xid v1.3.0
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.2.1
	github.com/spf13/viper v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/swaggo/echo-swagger v1.1.3
	github.com/swaggo/swag v1.7.1
	github.com/tidwall/gjson v1.10.2
	github.com/tidwall/sjson v1.1.7
	github.com/uber/jaeger-client-go v2.29.1+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible // indirect
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5
	golang.org/x/net v0.0.0-20211118161319-6a13c67c3ce4 // indirect
	google.golang.org/grpc v1.39.0
	gopkg.in/yaml.v2 v2.4.0
	xorm.io/xorm v1.1.2
)
