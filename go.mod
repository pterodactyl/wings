module github.com/pterodactyl/wings

go 1.13

// Uncomment this in development environments to make changes to the core SFTP
// server software. This assumes you're using the official Pterodactyl Environment
// otherwise this path will not work.
//
// @see https://github.com/pterodactyl/development
//
// replace github.com/pterodactyl/sftp-server => ../sftp-server

require (
	github.com/AlecAivazis/survey/v2 v2.0.7
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/Jeffail/gabs/v2 v2.2.0
	github.com/Microsoft/go-winio v0.4.7 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/apex/log v1.3.0
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/aws/aws-sdk-go v1.30.14 // indirect
	github.com/beevik/etree v1.1.0
	github.com/buger/jsonparser v0.0.0-20191204142016-1a29609e0929
	github.com/cobaugh/osrelease v0.0.0-20181218015638-a93a0a55a249
	github.com/containerd/fifo v0.0.0-20190226154929-a9fb20d87448 // indirect
	github.com/creasty/defaults v1.3.0
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v0.0.0-20180422163414-57142e89befe
	github.com/docker/go-connections v0.4.0
	github.com/docker/go-units v0.3.3 // indirect
	github.com/fatih/color v1.9.0
	github.com/gabriel-vasile/mimetype v0.1.4
	github.com/gbrlsnchs/jwt/v3 v3.0.0-rc.0
	github.com/ghodss/yaml v1.0.0
	github.com/gin-gonic/autotls v0.0.0-20200518075542-45033372a9ad
	github.com/gin-gonic/gin v1.6.3
	github.com/golang/protobuf v1.3.5 // indirect
	github.com/google/uuid v1.1.1
	github.com/gorilla/websocket v1.4.0
	github.com/gotestyourself/gotestyourself v2.2.0+incompatible // indirect
	github.com/iancoleman/strcase v0.0.0-20191112232945-16388991a334
	github.com/icza/dyno v0.0.0-20200205103839-49cb13720835
	github.com/imdario/mergo v0.3.8
	github.com/klauspost/pgzip v1.2.3
	github.com/magiconair/properties v1.8.1
	github.com/mattn/go-colorable v0.1.4
	github.com/mattn/go-shellwords v1.0.10 // indirect
	github.com/mholt/archiver/v3 v3.3.0
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.1 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/pkg/profile v1.4.0
	github.com/pkg/sftp v1.11.0 // indirect
	github.com/pterodactyl/sftp-server v1.1.4
	github.com/remeh/sizedwaitgroup v0.0.0-20180822144253-5e7302b12cce
	github.com/sabhiram/go-gitignore v0.0.0-20180611051255-d3107576ba94
	github.com/smartystreets/goconvey v1.6.4 // indirect
	github.com/spf13/cobra v0.0.7
	github.com/stretchr/objx v0.2.0 // indirect
	github.com/yuin/goldmark v1.1.30 // indirect
	go.uber.org/zap v1.15.0
	golang.org/x/crypto v0.0.0-20200429183012-4b2356b1ed79
	golang.org/x/lint v0.0.0-20200302205851-738671d3881b // indirect
	golang.org/x/net v0.0.0-20200425230154-ff2c4b7c35a0 // indirect
	golang.org/x/sync v0.0.0-20200317015054-43a5402ce75a
	golang.org/x/sys v0.0.0-20200509044756-6aff5f38e54f // indirect
	golang.org/x/tools v0.0.0-20200509030707-2212a7e161a5 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/ini.v1 v1.51.0
	gopkg.in/yaml.v2 v2.2.8
	gotest.tools v2.2.0+incompatible // indirect
	honnef.co/go/tools v0.0.1-2020.1.3 // indirect
)
