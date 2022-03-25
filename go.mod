module github.com/pterodactyl/wings

go 1.17

require (
	emperror.dev/errors v0.8.1
	github.com/AlecAivazis/survey/v2 v2.3.4
	github.com/Jeffail/gabs/v2 v2.6.1
	github.com/NYTimes/logrotate v1.0.0
	github.com/apex/log v1.9.0
	github.com/asaskevich/govalidator v0.0.0-20210307081110-f21760c49a8d
	github.com/beevik/etree v1.1.0
	github.com/buger/jsonparser v1.1.1
	github.com/cenkalti/backoff/v4 v4.1.2
	github.com/cobaugh/osrelease v0.0.0-20181218015638-a93a0a55a249
	github.com/creasty/defaults v1.5.2
	github.com/docker/docker v20.10.14+incompatible
	github.com/docker/go-connections v0.4.0
	github.com/fatih/color v1.13.0
	github.com/franela/goblin v0.0.0-20200825194134-80c0062ed6cd
	github.com/gabriel-vasile/mimetype v1.4.0
	github.com/gammazero/workerpool v1.1.2
	github.com/gbrlsnchs/jwt/v3 v3.0.1
	github.com/gin-gonic/gin v1.7.7
	github.com/google/uuid v1.3.0
	github.com/gorilla/websocket v1.5.0
	github.com/iancoleman/strcase v0.2.0
	github.com/icza/dyno v0.0.0-20210726202311-f1bafe5d9996
	github.com/juju/ratelimit v1.0.1
	github.com/karrick/godirwalk v1.16.1
	github.com/klauspost/pgzip v1.2.5
	github.com/magiconair/properties v1.8.6
	github.com/mattn/go-colorable v0.1.12
	github.com/mholt/archiver/v3 v3.5.1
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/sftp v1.13.4
	github.com/sabhiram/go-gitignore v0.0.0-20210923224102-525f6e181f06
	github.com/spf13/cobra v1.4.0
	github.com/stretchr/testify v1.7.0
	golang.org/x/crypto v0.0.0-20220321153916-2c7772ba3064
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	gopkg.in/ini.v1 v1.66.4
	gopkg.in/yaml.v2 v2.4.0
)

require github.com/goccy/go-json v0.9.6

require golang.org/x/sys v0.0.0-20220319134239-a9b59b0215f8 // indirect

require (
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/Microsoft/go-winio v0.5.2 // indirect
	github.com/Microsoft/hcsshim v0.9.2 // indirect
	github.com/andybalholm/brotli v1.0.4 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/containerd/containerd v1.6.2 // indirect
	github.com/containerd/fifo v1.0.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/distribution v2.8.1+incompatible // indirect
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/dsnet/compress v0.0.2-0.20210315054119-f66993602bf5 // indirect
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/gammazero/deque v0.1.1 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-playground/locales v0.14.0 // indirect
	github.com/go-playground/universal-translator v0.18.0 // indirect
	github.com/go-playground/validator/v10 v10.10.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/gorilla/mux v1.7.4 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/klauspost/compress v1.15.1 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/magefile/mage v1.13.0 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/moby/term v0.0.0-20210619224110-3f7ff695adc6 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/nwaples/rardecode v1.1.3 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.2 // indirect
	github.com/pierrec/lz4/v4 v4.1.14 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_golang v1.12.1 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.32.1 // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	github.com/sirupsen/logrus v1.8.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/ugorji/go/codec v1.2.7 // indirect
	github.com/ulikunitz/xz v0.5.10 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	go.uber.org/multierr v1.8.0 // indirect
	golang.org/x/net v0.0.0-20220225172249-27dd8689420f // indirect
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211 // indirect
	golang.org/x/text v0.3.7 // indirect
	golang.org/x/time v0.0.0-20220224211638-0e9765cccd65 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/genproto v0.0.0-20220324131243-acbaeb5b85eb // indirect
	google.golang.org/grpc v1.45.0 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)
