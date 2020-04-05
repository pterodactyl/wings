module github.com/pterodactyl/wings

go 1.12

// Uncomment this in development environments to make changes to the core SFTP
// server software. This assumes you're using the official Pterodactyl Environment
// otherwise this path will not work.
//
// @see https://github.com/pterodactyl/development
//
// replace github.com/pterodactyl/sftp-server => ../sftp-server

require (
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/Jeffail/gabs/v2 v2.2.0
	github.com/Microsoft/go-winio v0.4.7 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/beevik/etree v1.1.0
	github.com/buger/jsonparser v0.0.0-20191204142016-1a29609e0929
	github.com/containerd/fifo v0.0.0-20190226154929-a9fb20d87448 // indirect
	github.com/creasty/defaults v1.3.0
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v0.0.0-20180422163414-57142e89befe
	github.com/docker/go-connections v0.4.0
	github.com/docker/go-units v0.3.3 // indirect
	github.com/gabriel-vasile/mimetype v0.1.4
	github.com/gbrlsnchs/jwt/v3 v3.0.0-rc.0
	github.com/ghodss/yaml v1.0.0
	github.com/gogo/protobuf v1.2.1 // indirect
	github.com/google/uuid v1.1.1
	github.com/gorilla/websocket v1.4.0
	github.com/gotestyourself/gotestyourself v2.2.0+incompatible // indirect
	github.com/iancoleman/strcase v0.0.0-20191112232945-16388991a334
	github.com/imdario/mergo v0.3.8
	github.com/julienschmidt/httprouter v1.2.0
	github.com/magiconair/properties v1.8.1
	github.com/mattn/go-shellwords v1.0.10 // indirect
	github.com/mholt/archiver/v3 v3.3.0
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/onsi/ginkgo v1.8.0 // indirect
	github.com/onsi/gomega v1.5.0 // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.8.1
	github.com/pkg/sftp v1.10.1 // indirect
	github.com/pterodactyl/sftp-server v1.1.1
	github.com/remeh/sizedwaitgroup v0.0.0-20180822144253-5e7302b12cce
	github.com/sirupsen/logrus v1.0.5 // indirect
	github.com/smartystreets/goconvey v1.6.4 // indirect
	go.uber.org/atomic v1.5.1 // indirect
	go.uber.org/multierr v1.4.0 // indirect
	go.uber.org/zap v1.13.0
	golang.org/x/crypto v0.0.0-20191206172530-e9b2fee46413 // indirect
	golang.org/x/lint v0.0.0-20191125180803-fdd1cda4f05f // indirect
	golang.org/x/net v0.0.0-20190923162816-aa69164e4478 // indirect
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e // indirect
	golang.org/x/sys v0.0.0-20191206220618-eeba5f6aabab // indirect
	golang.org/x/text v0.3.2 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4 // indirect
	golang.org/x/tools v0.0.0-20191206204035-259af5ff87bd // indirect
	gopkg.in/airbrake/gobrake.v2 v2.0.9 // indirect
	gopkg.in/gemnasium/logrus-airbrake-hook.v2 v2.1.2 // indirect
	gopkg.in/ini.v1 v1.51.0
	gopkg.in/yaml.v2 v2.2.2
	gotest.tools v2.2.0+incompatible // indirect
)
